package backend

import (
	"encoding/json"
	"time"
)

// NAP-CONFIG: a napplet declares its settings as a JSON Schema, the launcher
// renders them in the napp's settings window, and the napplet reads what the
// user chose. The launcher is the only writer; nothing here takes a value
// from the napplet. Schema and values live in napconfig.go, keyed by the
// napp's address.
//
// config.values payloads may carry secrets: they are never logged.

// configOpenSettingsEvery rate-limits config.openSettings per window, so a
// napplet cannot keep throwing its settings window in the user's face.
const configOpenSettingsEvery = 2 * time.Second

func init() {
	handleNap(map[string]napHandler{
		"config.registerSchema": napConfigRegisterSchema,
		"config.get":            napConfigGet,
		"config.subscribe":      napConfigSubscribe,
		"config.unsubscribe":    napConfigUnsubscribe,
		"config.openSettings":   napConfigOpenSettings,
	})
}

func configSchemaErrorEnv(code, msg string) map[string]any {
	return map[string]any{"type": "config.schemaError", "code": code, "error": msg}
}

func napConfigRegisterSchema(c *napCall) {
	var r struct {
		Schema  json.RawMessage `json:"schema"`
		Version *float64        `json:"version"`
	}
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"ok": false, "code": cfgInvalidSchema, "error": "invalid request"})
		return
	}
	var version *uint64
	if r.Version != nil {
		if *r.Version < 0 || *r.Version != float64(uint64(*r.Version)) {
			c.reply(map[string]any{"ok": false, "code": cfgInvalidSchema, "error": "version must be a non-negative integer"})
			return
		}
		v := uint64(*r.Version)
		version = &v
	}
	napp := c.ci.napp
	changed, cerr := configRegister(napp.ID, napp.ArtifactHash, r.Schema, version)
	if cerr != nil {
		log.Info().Str("napplet", napp.ID).Str("code", cerr.Code).Str("error", cerr.Msg).Msg("napplet config schema rejected")
		c.reply(map[string]any{"ok": false, "code": cerr.Code, "error": cerr.Msg})
		// onSchemaError listeners hear about it too: the result is what
		// the registerSchema promise settles on
		c.ci.napPushGen(c.gen, configSchemaErrorEnv(cerr.Code, cerr.Msg))
		return
	}
	c.reply(map[string]any{"ok": true})
	if changed {
		// subscribers see the values the new schema resolves to; this
		// window's own included, after the result its registerSchema
		// was waiting for
		pushConfigValues(napp.ID)
		settingsChanged(napp.ID)
	}
}

func napConfigGet(c *napCall) {
	values, ok := configValues(c.ci.napp.ID)
	if !ok {
		// with the request's id, so the shim can settle the pending get
		c.replyAs("config.schemaError", map[string]any{"code": cfgNoSchema, "error": "no schema has been registered"})
		return
	}
	c.replyAs("config.values", map[string]any{"values": values})
}

func napConfigSubscribe(c *napCall) {
	s := c.ci.nap
	s.mu.Lock()
	s.configSubscribed = true
	s.mu.Unlock()
	values, ok := configValues(c.ci.napp.ID)
	if !ok {
		// the subscription stands: the first values arrive once a schema
		// is registered
		c.ci.napPushGen(c.gen, configSchemaErrorEnv(cfgNoSchema, "no schema has been registered"))
		return
	}
	c.ci.napPushGen(c.gen, map[string]any{"type": "config.values", "values": values})
}

func napConfigUnsubscribe(c *napCall) {
	s := c.ci.nap
	s.mu.Lock()
	s.configSubscribed = false
	s.mu.Unlock()
}

func napConfigOpenSettings(c *napCall) {
	var r struct {
		Section string `json:"section"`
	}
	_ = c.decode(&r)
	s := c.ci.nap
	s.mu.Lock()
	now := time.Now()
	if now.Sub(s.configOpenedAt) < configOpenSettingsEvery {
		s.mu.Unlock()
		return
	}
	s.configOpenedAt = now
	s.mu.Unlock()

	section := r.Section
	if section != "" {
		// an undeclared section is ignored silently: the window opens
		// at the top, and the napplet learns nothing either way
		if sch, _ := configSnapshot(c.ci.napp.ID); sch == nil || !sch.Sections[section] {
			section = ""
		}
	}
	nappID := c.ci.napp.ID
	go func() {
		if err := openSettings(nappID, section); err != nil {
			log.Warn().Err(err).Str("napplet", nappID).Msg("could not open napplet settings")
		}
	}()
}

// pushConfigValues gives every subscribed window of a napp its values.
func pushConfigValues(nappID string) {
	values, ok := configValues(nappID)
	if !ok {
		return
	}
	env := map[string]any{"type": "config.values", "values": values}
	for _, ci := range runningForNapp(nappID) {
		if ci.nap == nil {
			continue
		}
		ci.nap.mu.Lock()
		sub := ci.nap.configSubscribed
		ci.nap.mu.Unlock()
		if sub {
			ci.napPush(env)
		}
	}
}
