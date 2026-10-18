package backend

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// A picker is the window the user picks an action's handler in, when more
// than one napp could take it. It is a window of its own rather than a cover
// over the napp that fired the action, so that napp stays as it was, and so
// an action the launcher fires has somewhere to ask too.
//
// It is no napp's window: never listed, never recorded for reopening. Once
// the user picks, it either goes away (an open window was picked, or nothing
// was) or becomes the window of the napp that was picked, under its own
// instance id: in place where the platform can draw that napp in it, by a
// new window taking its place otherwise (see WindowSpec.Reuse).

// maxPickerIcon is the biggest icon a picker is sent: an icon that size is
// already an odd one, and every byte of it goes down the window's pipe.
const maxPickerIcon = 512 << 10

// openPicker puts up a picker window, or nil when the platform has none.
func openPicker() *Instance {
	themeName, themeVars := Theme()
	ci := &Instance{
		instance:   newInstanceID(),
		picker:     true,
		subs:       make(map[int]context.CancelFunc),
		actions:    make(map[string]int),
		changed:    make(chan struct{}),
		dispatches: make(map[int]chan WireMsg),
		gone:       make(chan struct{}),
	}
	registerInstance(ci)

	transport, err := host.OpenPicker(PickerSpec{
		Instance:  ci.instance,
		Theme:     themeName,
		ThemeVars: themeVars,
	})
	if err != nil {
		log.Debug().Err(err).Msg("no picker window, the picker covers the caller")
		WindowClosed(ci.instance)
		return nil
	}
	ci.attach(transport)
	return ci
}

// handOver takes the picker out of the launcher's windows without closing
// it, and gives its transport to whoever opens the napp that was picked in
// it (see launchWithInstance): from here on its instance id is that napp's.
func (ci *Instance) handOver() Transport {
	ci.goneOnce.Do(func() { close(ci.gone) })

	instancesMu.Lock()
	for i, c := range instances {
		if c == ci {
			instances = append(instances[:i], instances[i+1:]...)
			break
		}
	}
	instancesMu.Unlock()

	ci.sendMu.Lock()
	defer ci.sendMu.Unlock()
	return ci.transport
}

// pickerIcon is a napp's icon as a picker shows it: nil when it has none, or
// none that can be had quickly.
func pickerIcon(n Napp) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := n.IconBlob(ctx)
	if err != nil || len(data) > maxPickerIcon {
		return nil
	}
	return data
}

// ─── actions the launcher fires ──────────────────────────────────

// FireAction fires an action from the launcher itself, with the same routing
// napps get: a picker window opens when there is a choice to make. It
// returns right away; failures show up as the launcher's error.
func FireAction(action string, payload json.RawMessage) {
	go func() {
		if _, err := runNappAction(context.Background(), nil, action, payload, actionOptions{}); err != nil {
			log.Warn().Err(err).Str("action", action).Msg("launcher action failed")
			SetFetchErr(action + ": " + err.Error())
		}
	}()
}

// ViewProfile fires view:0 with the pubkey's profile event, so the user picks
// which napp shows it.
func ViewProfile(pubkeyHex string) {
	pk, err := nostr.PubKeyFromHex(pubkeyHex)
	if err != nil || sys == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var payload json.RawMessage
		if pm := sys.FetchProfileMetadata(ctx, pk); pm.Event != nil {
			payload, _ = json.Marshal(pm.Event)
		} else {
			payload, _ = json.Marshal(nip19.EncodeNaddr(pk, 0, "", nil))
		}
		FireAction("view:0", payload)
	}()
}

// ViewNappEvent fires view:<kind> with the napp's own event.
func ViewNappEvent(n Napp) {
	kind := n.KindOf()
	payload, _ := json.Marshal(nip19.EncodeNaddr(n.Author, kind, n.D, nil))
	FireAction("view:"+strconv.Itoa(int(kind)), payload)
}
