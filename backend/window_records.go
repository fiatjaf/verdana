package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Window records on disk: where a window outlives the process (on Android
// each napp window is a task of its own, which the OS keeps in Recents after
// killing the process), the backend has to remember the window across that
// death. The records are written to windows.json shortly after every change
// — the process can die at any moment without warning, so there is no saving
// on the way out — and read back on start. The OS then says which windows it
// still has (KeepWindows), and each of those rebuilds itself from its log
// when it comes back (RecoverWindow).

var (
	persistWindows  bool
	windowsPath     string
	windowsSaveMu   sync.Mutex
	windowsSaveTime *time.Timer
)

// windowsSaveDelay batches the writes of a burst of changes (a replay, a
// napp pushing its history on every keystroke) into one.
const windowsSaveDelay = 250 * time.Millisecond

func loadWindowRecords() {
	persistWindows = true
	windowsPath = filepath.Join(dataDir, "windows.json")
	data, err := os.ReadFile(windowsPath)
	if err != nil {
		return
	}
	var recs []windowRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		log.Warn().Err(err).Msg("unreadable windows.json, starting with no windows")
		return
	}
	for _, w := range recs {
		if w.Instance == "" || w.NappID == "" {
			continue
		}
		windows.Store(w.Instance, w)
	}
	log.Info().Int("windows", len(recs)).Msg("window records loaded")
}

// saveWindowRecords schedules a write of the records, if they are kept.
func saveWindowRecords() {
	if !persistWindows {
		return
	}
	windowsSaveMu.Lock()
	defer windowsSaveMu.Unlock()
	if windowsSaveTime != nil {
		return
	}
	windowsSaveTime = time.AfterFunc(windowsSaveDelay, func() {
		windowsSaveMu.Lock()
		windowsSaveTime = nil
		windowsSaveMu.Unlock()
		writeWindowRecords()
	})
}

func writeWindowRecords() {
	recs := make([]windowRecord, 0, windows.Size())
	for _, w := range windowRecords() {
		// an auxiliary window is a helper that closes once it answers: a
		// dead process doesn't bring it back
		if ci := lookupInstance(w.Instance); ci != nil && ci.auxiliary {
			continue
		}
		recs = append(recs, w)
	}
	data, err := json.Marshal(recs)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal window records")
		return
	}
	// written aside and renamed over, so a kill mid-write leaves the
	// previous file whole
	tmp := windowsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.Error().Err(err).Msg("failed to write window records")
		return
	}
	if err := os.Rename(tmp, windowsPath); err != nil {
		log.Error().Err(err).Msg("failed to write window records")
	}
}

// KeepWindows forgets every record of a window the OS no longer has, given
// the instances it still does: what was swiped away while the process was
// dead must not come back. Windows open in this process are always kept.
func KeepWindows(instances []string) {
	keep := make(map[string]bool, len(instances))
	for _, i := range instances {
		keep[i] = true
	}
	dropped := 0
	for _, w := range windowRecords() {
		if keep[w.Instance] || lookupInstance(w.Instance) != nil {
			continue
		}
		windows.Delete(w.Instance)
		dropped++
	}
	if dropped > 0 {
		saveWindowRecords()
		notifyState()
	}
}

// KnowsWindow says whether a window is open in this process. A window the
// OS brings back after killing the process is not, and has to be recovered.
func KnowsWindow(instance string) bool { return lookupInstance(instance) != nil }

// RecoverWindow rebuilds a window the OS kept after killing the process:
// opened again under its own instance and replayed back to where its log
// says it was, as a reopen. The error means there is nothing to rebuild it
// from.
func RecoverWindow(instance string) error {
	if KnowsWindow(instance) {
		return nil
	}
	log.Info().Str("instance", instance).Msg("recovering napp window")
	return ReopenWindow(instance)
}
