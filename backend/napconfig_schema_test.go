package backend

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func loadConfigFixture(t *testing.T) *configSchema {
	t.Helper()
	raw, err := os.ReadFile("testdata/config/full.json")
	if err != nil {
		t.Fatal(err)
	}
	s, cerr := checkConfigSchema(raw)
	if cerr != nil {
		t.Fatalf("fixture rejected: %v", cerr)
	}
	return s
}

func TestConfigSchemaFixtureAccepted(t *testing.T) {
	s := loadConfigFixture(t)
	if s.Version == nil || *s.Version != 2 {
		t.Fatalf("$version = %v, want 2", s.Version)
	}
	if !s.Sections["appearance"] || !s.Sections["notifications"] || len(s.Sections) != 2 {
		t.Fatalf("sections = %v", s.Sections)
	}
	if !s.Root.Props["apiKey"].Secret {
		t.Fatal("apiKey should be a secret")
	}
}

func TestConfigSchemaRejections(t *testing.T) {
	nest := func(depth int) string {
		s := `{"type":"string"}`
		for i := 0; i < depth-1; i++ {
			s = `{"type":"object","properties":{"x":` + s + `}}`
		}
		return `{"type":"object","properties":{"x":` + s + `}}`
	}
	cases := []struct {
		name, schema, code string
	}{
		{"not json", `{`, cfgInvalidSchema},
		{"not an object", `[]`, cfgInvalidSchema},
		{"root not object", `{"type":"string"}`, cfgInvalidSchema},
		{"root without properties", `{"type":"object"}`, cfgInvalidSchema},
		{"draft-04", `{"$schema":"http://json-schema.org/draft-04/schema#","type":"object","properties":{}}`, cfgUnsupportedDraft},
		{"$ref", `{"type":"object","properties":{"a":{"$ref":"#/definitions/a"}}}`, cfgRefNotAllowed},
		{"definitions", `{"type":"object","properties":{},"definitions":{}}`, cfgRefNotAllowed},
		{"$defs", `{"type":"object","properties":{},"$defs":{}}`, cfgRefNotAllowed},
		{"pattern", `{"type":"object","properties":{"u":{"type":"string","pattern":"^[a-z]+$"}}}`, cfgPatternNotAllowed},
		{"oneOf", `{"type":"object","properties":{"a":{"oneOf":[]}}}`, cfgInvalidSchema},
		{"if", `{"type":"object","properties":{},"if":{}}`, cfgInvalidSchema},
		{"tuple", `{"type":"object","properties":{"a":{"type":"array","items":[{"type":"string"}]}}}`, cfgInvalidSchema},
		{"array of objects", `{"type":"object","properties":{"a":{"type":"array","items":{"type":"object","properties":{}}}}}`, cfgInvalidSchema},
		{"type list", `{"type":"object","properties":{"a":{"type":["string","null"]}}}`, cfgInvalidSchema},
		{"null type", `{"type":"object","properties":{"a":{"type":"null"}}}`, cfgInvalidSchema},
		{"secret with default", `{"type":"object","properties":{"k":{"type":"string","x-napplet-secret":true,"default":"x"}}}`, cfgSecretWithDefault},
		{"too deep", nest(5), cfgTooDeep}, // root + four nested objects
		{"bad default", `{"type":"object","properties":{"n":{"type":"integer","minimum":1,"default":0}}}`, cfgInvalidSchema},
		{"enum type mismatch", `{"type":"object","properties":{"n":{"type":"string","enum":[1]}}}`, cfgInvalidSchema},
		{"negative minLength", `{"type":"object","properties":{"s":{"type":"string","minLength":-1}}}`, cfgInvalidSchema},
		{"additionalProperties schema", `{"type":"object","properties":{},"additionalProperties":{"type":"string"}}`, cfgInvalidSchema},
		{"bad $version", `{"type":"object","properties":{},"$version":1.5}`, cfgInvalidSchema},
		{"too big", `{"type":"object","properties":{},"description":"` + strings.Repeat("x", configSchemaMax) + `"}`, cfgInvalidSchema},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := checkConfigSchema(json.RawMessage(c.schema))
			if err == nil {
				t.Fatal("accepted")
			}
			if err.Code != c.code {
				t.Fatalf("code = %s (%s), want %s", err.Code, err.Msg, c.code)
			}
		})
	}
}

func TestConfigSchemaDepthLimit(t *testing.T) {
	// root + three nested objects: four levels, the limit
	ok := `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","properties":{"d":{"type":"string"}}}}}}}}}`
	if _, err := checkConfigSchema(json.RawMessage(ok)); err != nil {
		t.Fatalf("depth 4 rejected: %v", err)
	}
}

func TestConfigSchemaOpaqueExtensions(t *testing.T) {
	s := `{"type":"object","properties":{"a":{"type":"string","format":"color","x-napplet-widget":"wheel","deprecationMessage":"old","markdownDescription":"*hi*"}}}`
	if _, err := checkConfigSchema(json.RawMessage(s)); err != nil {
		t.Fatalf("unknown extensions must be opaque: %v", err)
	}
}

func TestConfigResolveDefaults(t *testing.T) {
	s := loadConfigFixture(t)
	got := resolveConfigValues(s, nil)
	want := map[string]any{
		"theme":    "dark",
		"fontSize": float64(14),
		"relays":   []any{"wss://relay.example"},
		// enabled has its own default; sound and volume come from the
		// object's; email has neither
		"notifications": map[string]any{"enabled": true, "sound": false, "volume": 0.5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("defaults:\n got %#v\nwant %#v", got, want)
	}
}

func TestConfigResolveStoredAndInvalid(t *testing.T) {
	s := loadConfigFixture(t)
	stored := map[string]any{
		"theme":    "light",
		"fontSize": float64(99), // out of range: the default instead
		"relays":   []any{"wss://a.example", 3.0},
		"apiKey":   "sekret",
		"orphan":   "never delivered",
		"notifications": map[string]any{
			"volume": 0.9,
			"email":  "not-an-email", // format is a hint only
			"ghost":  true,
		},
	}
	got := resolveConfigValues(s, stored)
	want := map[string]any{
		"theme":    "light",
		"fontSize": float64(14),
		"relays":   []any{"wss://relay.example"},
		"apiKey":   "sekret",
		"notifications": map[string]any{
			"enabled": true, "sound": false, "volume": 0.9, "email": "not-an-email",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved:\n got %#v\nwant %#v", got, want)
	}
}

func TestConfigSecretNeverDefaulted(t *testing.T) {
	s := loadConfigFixture(t)
	got := resolveConfigValues(s, map[string]any{"apiKey": "abc"}) // too short
	if _, ok := got["apiKey"]; ok {
		t.Fatal("an invalid or unset secret must not be delivered")
	}
}

func TestConfigMerge(t *testing.T) {
	s := loadConfigFixture(t)
	stored := map[string]any{"theme": "light", "apiKey": "sekret", "orphan": 1.0}

	// the page leaves the secret out: kept; leaves theme out: unset
	next, err := mergeConfigValues(s, stored, map[string]any{"fontSize": 20.0})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"fontSize": 20.0, "apiKey": "sekret"}
	if !reflect.DeepEqual(next, want) {
		t.Fatalf("merge: got %#v want %#v", next, want)
	}

	// null clears a secret
	next, err = mergeConfigValues(s, stored, map[string]any{"apiKey": nil})
	if err != nil || len(next) != 0 {
		t.Fatalf("clearing the secret: %v %#v", err, next)
	}

	for _, bad := range []map[string]any{
		{"orphan": 1.0},
		{"theme": "blue"},
		{"fontSize": 14.5},
		{"notifications": "on"},
		{"notifications": map[string]any{"ghost": true}},
	} {
		if _, err := mergeConfigValues(s, stored, bad); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
}

func TestConfigRequired(t *testing.T) {
	s, cerr := checkConfigSchema(json.RawMessage(`{"type":"object","required":["name"],"properties":{"name":{"type":"string"},"opt":{"type":"string"}}}`))
	if cerr != nil {
		t.Fatal(cerr)
	}
	if err := checkConfigRequired(s, map[string]any{"opt": "x"}); err == nil {
		t.Fatal("a missing required property must fail the save")
	}
	if err := checkConfigRequired(s, map[string]any{"name": "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPruneSecretOrphans(t *testing.T) {
	old := loadConfigFixture(t)
	next, cerr := checkConfigSchema(json.RawMessage(`{"type":"object","properties":{"theme":{"type":"string"},"apiKey":{"type":"string"}}}`))
	if cerr != nil {
		t.Fatal(cerr)
	}
	stored := map[string]any{"theme": "light", "apiKey": "sekret", "fontSize": 20.0}
	got := pruneSecretOrphans(old.Root, next.Root, stored)
	// apiKey stopped being a secret: gone at once; fontSize is a plain
	// orphan, kept until the next save
	want := map[string]any{"theme": "light", "fontSize": 20.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestConfigStore(t *testing.T) {
	setupNapTest(t)
	const id = "napplet~0123456789abcdef~cfg"
	if _, ok := configValues(id); ok {
		t.Fatal("values before any schema")
	}
	raw, _ := os.ReadFile("testdata/config/full.json")
	if changed, err := configRegister(id, "hash1", raw, nil); err != nil || !changed {
		t.Fatalf("register: %v %v", changed, err)
	}
	if changed, err := configRegister(id, "hash1", raw, nil); err != nil || changed {
		t.Fatalf("re-register of the same schema: %v %v", changed, err)
	}
	v1 := uint64(1)
	if _, err := configRegister(id, "hash1", raw, &v1); err == nil || err.Code != cfgVersionConflict {
		t.Fatalf("version disagreeing with $version: %v", err)
	}
	if err := configSave(id, map[string]any{"theme": "light", "apiKey": "sekret"}); err != nil {
		t.Fatal(err)
	}

	// a fresh process reads it back, and a new artifact keeps the values
	configMu.Lock()
	configs = make(map[string]*configEntry)
	configMu.Unlock()
	if _, err := configRegister(id, "hash2", raw, nil); err != nil {
		t.Fatal(err)
	}
	vals, ok := configValues(id)
	if !ok || vals["theme"] != "light" || vals["apiKey"] != "sekret" {
		t.Fatalf("after reload: %v %#v", ok, vals)
	}
	if err := configReset(id); err != nil {
		t.Fatal(err)
	}
	vals, _ = configValues(id)
	if vals["theme"] != "dark" || vals["apiKey"] != nil {
		t.Fatalf("after reset: %#v", vals)
	}
}
