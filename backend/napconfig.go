package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// The NAP-CONFIG store: per napp, the schema it last registered and the
// values the user set. It is keyed by the napp's address, not by artifact
// like NAP-STORAGE: values are re-validated against the current schema
// every time they are delivered, which is all the migration NAP-CONFIG asks
// of a shell that carries settings across versions ($version is a signal
// only), and a user's settings surviving an update is the point.
//
// The launcher is the only writer of values: napplets register schemas and
// read; the settings page saves.

type configRecord struct {
	Schema       json.RawMessage `json:"schema,omitempty"`
	ArtifactHash string          `json:"artifactHash,omitempty"`
	Values       map[string]any  `json:"values,omitempty"`
}

type configEntry struct {
	rec    configRecord
	schema *configSchema // nil: none registered (or the stored one is unreadable)
}

var (
	configMu sync.Mutex
	configs  = make(map[string]*configEntry)
)

func configFileFor(nappID string) string {
	return filepath.Join(dataDir, "config", safeFileName(nappID)+".json")
}

// configLocked loads a napp's entry on first use; configMu is held.
func configLocked(nappID string) *configEntry {
	if e, ok := configs[nappID]; ok {
		return e
	}
	e := &configEntry{}
	if raw, err := os.ReadFile(configFileFor(nappID)); err == nil {
		if err := json.Unmarshal(raw, &e.rec); err != nil {
			log.Warn().Err(err).Str("napp", nappID).Msg("unreadable napplet config, starting empty")
			e.rec = configRecord{}
		}
		if len(e.rec.Schema) > 0 {
			// stored schemas were checked when registered; a failure here
			// is a stricter launcher, and the napplet registers again anyway
			e.schema, _ = checkConfigSchema(e.rec.Schema)
		}
	}
	configs[nappID] = e
	return e
}

func configPersistLocked(nappID string, rec configRecord) error {
	dir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, configFileFor(nappID)); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// configRegister checks and stores a schema for a napp. version, when the
// napplet passed one, stands in for the schema's $version. A schema
// identical to the stored one is a no-op that reports changed=false.
func configRegister(nappID, artifactHash string, raw json.RawMessage, version *uint64) (changed bool, cerr *configSchemaError) {
	s, cerr := checkConfigSchema(raw)
	if cerr != nil {
		return false, cerr
	}
	if version != nil {
		if s.Version != nil && *s.Version != *version {
			return false, schemaErr(cfgVersionConflict, "version %d disagrees with the schema's $version %d", *version, *s.Version)
		}
		s.Version = version
	}

	configMu.Lock()
	defer configMu.Unlock()
	e := configLocked(nappID)
	if e.schema != nil && e.rec.ArtifactHash == artifactHash &&
		e.schema.Version != nil && s.Version != nil && *s.Version < *e.schema.Version {
		// the same artifact going back a version is a napplet confused
		// about its own schema, not an update
		return false, schemaErr(cfgVersionConflict, "version %d is older than the registered %d", *s.Version, *e.schema.Version)
	}
	if e.schema != nil && e.rec.ArtifactHash == artifactHash && jsonEqual(e.rec.Schema, raw) {
		return false, nil
	}
	rec := configRecord{Schema: s.Raw, ArtifactHash: artifactHash, Values: e.rec.Values}
	if e.schema != nil {
		rec.Values = pruneSecretOrphans(e.schema.Root, s.Root, rec.Values)
	}
	if err := configPersistLocked(nappID, rec); err != nil {
		log.Error().Err(err).Str("napp", nappID).Msg("could not persist a napplet config schema")
		return false, schemaErr(cfgInvalidSchema, "the launcher could not store the schema")
	}
	e.rec, e.schema = rec, s
	return true, nil
}

// configValues is what the napp is delivered now; ok is false while it has
// no schema.
func configValues(nappID string) (values map[string]any, ok bool) {
	configMu.Lock()
	defer configMu.Unlock()
	e := configLocked(nappID)
	if e.schema == nil {
		return nil, false
	}
	return resolveConfigValues(e.schema, e.rec.Values), true
}

// configSnapshot is the schema and the stored values, for the settings page.
func configSnapshot(nappID string) (*configSchema, map[string]any) {
	configMu.Lock()
	defer configMu.Unlock()
	e := configLocked(nappID)
	return e.schema, e.rec.Values
}

// errNoConfigSchema is a save for a napp that never registered a schema.
var errNoConfigSchema = &configSchemaError{Code: cfgNoSchema, Msg: "this napplet has no settings"}

// configSave stores what the settings page saved, if it all validates.
func configSave(nappID string, in map[string]any) error {
	configMu.Lock()
	defer configMu.Unlock()
	e := configLocked(nappID)
	if e.schema == nil {
		return errNoConfigSchema
	}
	next, err := mergeConfigValues(e.schema, e.rec.Values, in)
	if err != nil {
		return err
	}
	if err := checkConfigRequired(e.schema, resolveConfigValues(e.schema, next)); err != nil {
		return err
	}
	rec := e.rec
	rec.Values = next
	if err := configPersistLocked(nappID, rec); err != nil {
		return err
	}
	e.rec = rec
	return nil
}

// configReset drops every value, so defaults apply again.
func configReset(nappID string) error {
	configMu.Lock()
	defer configMu.Unlock()
	e := configLocked(nappID)
	if len(e.rec.Values) == 0 {
		return nil
	}
	rec := e.rec
	rec.Values = nil
	if err := configPersistLocked(nappID, rec); err != nil {
		return err
	}
	e.rec = rec
	return nil
}

// HasConfigSchema is a napp having registered settings, for the platforms'
// "Settings" buttons.
func HasConfigSchema(nappID string) bool {
	configMu.Lock()
	defer configMu.Unlock()
	return configLocked(nappID).schema != nil
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ra, _ := json.Marshal(x)
	rb, _ := json.Marshal(y)
	return string(ra) == string(rb)
}
