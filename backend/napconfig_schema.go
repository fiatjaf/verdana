package backend

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

// NAP-CONFIG schemas: the Core Subset of JSON Schema a napplet may declare
// its settings with. checkConfigSchema turns the napplet's JSON into a
// configNode tree, rejecting anything outside the subset with the NAP's
// error codes; the tree is then all the validator and the settings page
// ever look at. Nothing in a schema is ever run: no $ref, no pattern.

const (
	// configSchemaMax caps a schema's size, before it is even parsed.
	configSchemaMax = 64 * 1024
	// configMaxDepth is how deep objects may nest, the root counting as 1.
	configMaxDepth = 4
)

// the schema error codes (NAP-CONFIG "Error codes")
const (
	cfgInvalidSchema     = "invalid-schema"
	cfgUnsupportedDraft  = "unsupported-draft"
	cfgRefNotAllowed     = "ref-not-allowed"
	cfgPatternNotAllowed = "pattern-not-allowed"
	cfgSecretWithDefault = "secret-with-default"
	cfgTooDeep           = "schema-too-deep"
	cfgVersionConflict   = "version-conflict"
	cfgNoSchema          = "no-schema"
)

// configSchemaError is a rejected schema: a code from the list above and a
// message for the napplet's developer.
type configSchemaError struct {
	Code string
	Msg  string
}

func (e *configSchemaError) Error() string { return e.Code + ": " + e.Msg }

func schemaErr(code, format string, args ...any) *configSchemaError {
	return &configSchemaError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// configNode is one checked schema node.
type configNode struct {
	Type string

	Default    any
	HasDefault bool
	Enum       []any

	Minimum, Maximum     *float64
	MinLength, MaxLength *int
	MinItems, MaxItems   *int

	// Items is an array's element schema (a primitive, always).
	Items *configNode
	// Props and Required are an object's.
	Props    map[string]*configNode
	Required []string

	// Secret is x-napplet-secret on a string: never defaulted, never
	// delivered unless the user set it, masked in the settings page.
	Secret bool
}

// configSchema is a schema that passed the check.
type configSchema struct {
	Root *configNode
	// Raw is the schema as the napplet sent it, for the settings page and
	// for napplets reading it back.
	Raw json.RawMessage
	// Version is $version, when the schema has one.
	Version *uint64
	// Sections are the x-napplet-section names it declares.
	Sections map[string]bool
}

// keywords outside the Core Subset, rejected wherever they appear
var configForbidden = map[string]string{
	"$ref":                  cfgRefNotAllowed,
	"$dynamicRef":           cfgRefNotAllowed,
	"$recursiveRef":         cfgRefNotAllowed,
	"definitions":           cfgRefNotAllowed,
	"$defs":                 cfgRefNotAllowed,
	"pattern":               cfgPatternNotAllowed,
	"patternProperties":     cfgInvalidSchema,
	"oneOf":                 cfgInvalidSchema,
	"anyOf":                 cfgInvalidSchema,
	"allOf":                 cfgInvalidSchema,
	"not":                   cfgInvalidSchema,
	"if":                    cfgInvalidSchema,
	"then":                  cfgInvalidSchema,
	"else":                  cfgInvalidSchema,
	"propertyNames":         cfgInvalidSchema,
	"dependencies":          cfgInvalidSchema,
	"dependentSchemas":      cfgInvalidSchema,
	"dependentRequired":     cfgInvalidSchema,
	"unevaluatedProperties": cfgInvalidSchema,
	"unevaluatedItems":      cfgInvalidSchema,
	"prefixItems":           cfgInvalidSchema,
	"additionalItems":       cfgInvalidSchema,
	"contains":              cfgInvalidSchema,
}

// the drafts a $schema may name: draft-07 or later
var configDrafts = []string{
	"json-schema.org/draft-07/schema",
	"json-schema.org/draft/2019-09/schema",
	"json-schema.org/draft/2020-12/schema",
}

// checkConfigSchema checks a napplet's schema against the Core Subset.
func checkConfigSchema(raw json.RawMessage) (*configSchema, *configSchemaError) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, schemaErr(cfgInvalidSchema, "no schema")
	}
	if len(raw) > configSchemaMax {
		return nil, schemaErr(cfgInvalidSchema, "schema is larger than %d bytes", configSchemaMax)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, schemaErr(cfgInvalidSchema, "schema is not valid JSON")
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, schemaErr(cfgInvalidSchema, "schema must be a JSON object")
	}
	if s, ok := m["$schema"]; ok {
		uri, _ := s.(string)
		known := false
		for _, d := range configDrafts {
			if strings.Contains(uri, d) {
				known = true
				break
			}
		}
		if !known {
			return nil, schemaErr(cfgUnsupportedDraft, "unsupported $schema %q", uri)
		}
	}
	if m["type"] != "object" {
		return nil, schemaErr(cfgInvalidSchema, "schema root must be of type `object`")
	}
	out := &configSchema{Raw: append(json.RawMessage(nil), raw...), Sections: map[string]bool{}}
	if v, ok := m["$version"]; ok {
		n, ok := v.(float64)
		if !ok || n < 0 || n != math.Trunc(n) || n > 1<<53 {
			return nil, schemaErr(cfgInvalidSchema, "$version must be a non-negative integer")
		}
		u := uint64(n)
		out.Version = &u
	}
	root, err := checkConfigNode(m, "", 1, out.Sections)
	if err != nil {
		return nil, err
	}
	out.Root = root
	return out, nil
}

func checkConfigNode(m map[string]any, path string, depth int, sections map[string]bool) (*configNode, *configSchemaError) {
	where := path
	if where == "" {
		where = "the root"
	}
	// sorted, so the same schema always fails on the same keyword
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if code, bad := configForbidden[k]; bad {
			return nil, schemaErr(code, "`%s` (at %s) is not permitted in the Core Subset", k, where)
		}
	}

	n := &configNode{}
	t, ok := m["type"].(string)
	if !ok {
		return nil, schemaErr(cfgInvalidSchema, "%s needs a single `type`", where)
	}
	n.Type = t

	if sec, ok := m["x-napplet-section"].(string); ok && sec != "" {
		sections[sec] = true
	}
	if secret, _ := m["x-napplet-secret"].(bool); secret {
		if _, has := m["default"]; has {
			return nil, schemaErr(cfgSecretWithDefault, "%s is x-napplet-secret and so cannot have a default", where)
		}
		// a secret is a string thing; on anything else it is opaque metadata
		n.Secret = t == "string"
	}

	var err *configSchemaError
	switch t {
	case "string", "number", "integer", "boolean":
	case "array":
		if depth == 0 {
			// an array's items
			return nil, schemaErr(cfgInvalidSchema, "%s: arrays of arrays are not in the Core Subset", where)
		}
		im, ok := m["items"].(map[string]any)
		if !ok {
			return nil, schemaErr(cfgInvalidSchema, "%s needs `items` as one schema (no tuples)", where)
		}
		if it, _ := im["type"].(string); it == "object" || it == "array" {
			return nil, schemaErr(cfgInvalidSchema, "%s: only arrays of primitives are in the Core Subset", where)
		}
		if n.Items, err = checkConfigNode(im, path+"[]", 0, sections); err != nil {
			return nil, err
		}
		if n.MinItems, err = schemaCount(m, "minItems", where); err != nil {
			return nil, err
		}
		if n.MaxItems, err = schemaCount(m, "maxItems", where); err != nil {
			return nil, err
		}
	case "object":
		if depth == 0 {
			return nil, schemaErr(cfgInvalidSchema, "%s: arrays of objects are not in the Core Subset", where)
		}
		if depth > configMaxDepth {
			return nil, schemaErr(cfgTooDeep, "objects nest deeper than %d levels at %s", configMaxDepth, where)
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			if _, has := m["properties"]; has || path == "" {
				return nil, schemaErr(cfgInvalidSchema, "%s needs `properties` as an object", where)
			}
		}
		if ap, has := m["additionalProperties"]; has {
			if _, ok := ap.(bool); !ok {
				return nil, schemaErr(cfgInvalidSchema, "%s: `additionalProperties` must be a boolean", where)
			}
		}
		n.Props = make(map[string]*configNode, len(props))
		for name, raw := range props {
			pm, ok := raw.(map[string]any)
			if !ok {
				return nil, schemaErr(cfgInvalidSchema, "property %q is not a schema", name)
			}
			child, err := checkConfigNode(pm, joinConfigPath(path, name), depth+1, sections)
			if err != nil {
				return nil, err
			}
			n.Props[name] = child
		}
		if req, has := m["required"]; has {
			list, ok := req.([]any)
			if !ok {
				return nil, schemaErr(cfgInvalidSchema, "%s: `required` must be an array", where)
			}
			for _, r := range list {
				s, ok := r.(string)
				if !ok {
					return nil, schemaErr(cfgInvalidSchema, "%s: `required` must list property names", where)
				}
				n.Required = append(n.Required, s)
			}
		}
	default:
		return nil, schemaErr(cfgInvalidSchema, "%s has unsupported type %q", where, t)
	}

	if n.Minimum, err = schemaNumber(m, "minimum", where); err != nil {
		return nil, err
	}
	if n.Maximum, err = schemaNumber(m, "maximum", where); err != nil {
		return nil, err
	}
	if n.MinLength, err = schemaCount(m, "minLength", where); err != nil {
		return nil, err
	}
	if n.MaxLength, err = schemaCount(m, "maxLength", where); err != nil {
		return nil, err
	}
	if e, has := m["enum"]; has {
		list, ok := e.([]any)
		if !ok || len(list) == 0 {
			return nil, schemaErr(cfgInvalidSchema, "%s: `enum` must be a non-empty array", where)
		}
		n.Enum = list
		for _, v := range list {
			if !n.validType(v) {
				return nil, schemaErr(cfgInvalidSchema, "%s: an `enum` value does not match its type", where)
			}
		}
	}
	if d, has := m["default"]; has {
		n.Default, n.HasDefault = d, true
		if !n.valid(d) {
			return nil, schemaErr(cfgInvalidSchema, "%s: `default` does not validate against its own schema", where)
		}
	}
	return n, nil
}

func joinConfigPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func schemaNumber(m map[string]any, key, where string) (*float64, *configSchemaError) {
	v, has := m[key]
	if !has {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil, schemaErr(cfgInvalidSchema, "%s: `%s` must be a number", where, key)
	}
	return &f, nil
}

func schemaCount(m map[string]any, key, where string) (*int, *configSchemaError) {
	v, has := m[key]
	if !has {
		return nil, nil
	}
	f, ok := v.(float64)
	if !ok || f < 0 || f != math.Trunc(f) || f > math.MaxInt32 {
		return nil, schemaErr(cfgInvalidSchema, "%s: `%s` must be a non-negative integer", where, key)
	}
	i := int(f)
	return &i, nil
}

// ─── values ──────────────────────────────────────────────────────

// validType is v's JSON type matching the node's, nothing more.
func (n *configNode) validType(v any) bool {
	switch n.Type {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f) && !math.IsInf(f, 0)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	}
	return false
}

// valid is v validating against the node. Objects are valid when every
// property they carry is declared and valid, and every required one is
// there; format is a hint, never checked.
func (n *configNode) valid(v any) bool {
	if !n.validType(v) {
		return false
	}
	if n.Enum != nil {
		found := false
		for _, e := range n.Enum {
			if reflect.DeepEqual(e, v) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	switch n.Type {
	case "string":
		l := utf8.RuneCountInString(v.(string))
		if n.MinLength != nil && l < *n.MinLength || n.MaxLength != nil && l > *n.MaxLength {
			return false
		}
	case "number", "integer":
		f := v.(float64)
		if n.Minimum != nil && f < *n.Minimum || n.Maximum != nil && f > *n.Maximum {
			return false
		}
	case "array":
		list := v.([]any)
		if n.MinItems != nil && len(list) < *n.MinItems || n.MaxItems != nil && len(list) > *n.MaxItems {
			return false
		}
		for _, it := range list {
			if !n.Items.valid(it) {
				return false
			}
		}
	case "object":
		obj := v.(map[string]any)
		for k, pv := range obj {
			p, ok := n.Props[k]
			if !ok || !p.valid(pv) {
				return false
			}
		}
		for _, r := range n.Required {
			if _, ok := obj[r]; !ok {
				return false
			}
		}
	}
	return true
}

// resolveConfigValues is what a napplet is delivered: per property, the
// stored value if it validates, else its own default, else what an
// ancestor's default says for it, else nothing (NAP-CONFIG's
// default-resolution rule). Undeclared keys are never delivered, and a
// secret only ever comes from the store.
func resolveConfigValues(s *configSchema, stored map[string]any) map[string]any {
	if s == nil {
		return nil
	}
	out := resolveConfigObject(s.Root, stored, nil)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func resolveConfigObject(n *configNode, stored, inherited map[string]any) map[string]any {
	out := map[string]any{}
	for k, p := range n.Props {
		sv, has := stored[k]
		if p.Type == "object" {
			sub, _ := sv.(map[string]any)
			// the nearest default wins: the property's own, else what the
			// ancestor's says for it
			anc, _ := p.Default.(map[string]any)
			if !p.HasDefault {
				anc, _ = inherited[k].(map[string]any)
			}
			if v := resolveConfigObject(p, sub, anc); len(v) > 0 {
				out[k] = v
			}
			continue
		}
		switch {
		case has && p.valid(sv):
			out[k] = sv
		case p.Secret:
			// set by the user or not at all
		case p.HasDefault:
			out[k] = p.Default
		default:
			if iv, ok := inherited[k]; ok && p.valid(iv) {
				out[k] = iv
			}
		}
	}
	return out
}

// mergeConfigValues applies what the settings page saved to what was stored.
// The page sends the whole form: a property it leaves out is unset (its
// default applies again), except a secret, which it never sees and so
// keeps unless the page sends null for it. Every value must validate; an
// undeclared key is an error. Orphans from an older schema go with the
// save.
func mergeConfigValues(s *configSchema, stored, in map[string]any) (map[string]any, error) {
	return mergeConfigObject(s.Root, "", stored, in)
}

func mergeConfigObject(n *configNode, path string, stored, in map[string]any) (map[string]any, error) {
	for k := range in {
		if _, ok := n.Props[k]; !ok {
			return nil, fmt.Errorf("%s is not a setting", joinConfigPath(path, k))
		}
	}
	out := map[string]any{}
	for k, p := range n.Props {
		name := joinConfigPath(path, k)
		iv, has := in[k]
		if p.Type == "object" {
			sub, _ := iv.(map[string]any)
			if has && iv != nil && sub == nil {
				return nil, fmt.Errorf("%s must be an object", name)
			}
			old, _ := stored[k].(map[string]any)
			v, err := mergeConfigObject(p, name, old, sub)
			if err != nil {
				return nil, err
			}
			if len(v) > 0 {
				out[k] = v
			}
			continue
		}
		switch {
		case p.Secret && !has:
			if sv, ok := stored[k]; ok && p.valid(sv) {
				out[k] = sv
			}
		case !has || iv == nil:
		case !p.valid(iv):
			return nil, fmt.Errorf("%s is not a valid value", name)
		default:
			out[k] = iv
		}
	}
	return out, nil
}

// checkConfigRequired is every required property resolving to something.
func checkConfigRequired(s *configSchema, values map[string]any) error {
	return checkRequiredObject(s.Root, "", values)
}

func checkRequiredObject(n *configNode, path string, values map[string]any) error {
	for _, r := range n.Required {
		if _, ok := values[r]; !ok {
			if p, declared := n.Props[r]; declared && p.Type == "object" {
				// an object with nothing set is still there, just empty
				if err := checkRequiredObject(p, joinConfigPath(path, r), nil); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("%s is required", joinConfigPath(path, r))
		}
	}
	for k, p := range n.Props {
		if p.Type != "object" {
			continue
		}
		sub, _ := values[k].(map[string]any)
		if sub == nil && !requiredIn(n, k) {
			continue
		}
		if err := checkRequiredObject(p, joinConfigPath(path, k), sub); err != nil {
			return err
		}
	}
	return nil
}

func requiredIn(n *configNode, k string) bool {
	for _, r := range n.Required {
		if r == k {
			return true
		}
	}
	return false
}

// pruneSecretOrphans drops stored secrets the new schema no longer declares
// as secrets: NAP-CONFIG wants those gone the moment the schema changes.
// Non-secret orphans stay on disk (never delivered) until the next save.
func pruneSecretOrphans(old, next *configNode, stored map[string]any) map[string]any {
	if old == nil || stored == nil {
		return stored
	}
	out := make(map[string]any, len(stored))
	for k, v := range stored {
		op := old.Props[k]
		var np *configNode
		if next != nil {
			np = next.Props[k]
		}
		switch {
		case op == nil:
			out[k] = v
		case op.Type == "object":
			sub, _ := v.(map[string]any)
			var nextObj *configNode
			if np != nil && np.Type == "object" {
				nextObj = np
			}
			if p := pruneSecretOrphans(op, nextObj, sub); len(p) > 0 {
				out[k] = p
			}
		case op.Secret && (np == nil || !np.Secret):
			// gone
		default:
			out[k] = v
		}
	}
	return out
}

// configSecretsSet lists the dotted paths of the secrets the user has set,
// for the settings page, which never gets their values.
func configSecretsSet(n *configNode, path string, stored map[string]any) []string {
	var out []string
	for k, p := range n.Props {
		name := joinConfigPath(path, k)
		switch {
		case p.Type == "object":
			sub, _ := stored[k].(map[string]any)
			out = append(out, configSecretsSet(p, name, sub)...)
		case p.Secret:
			if sv, ok := stored[k]; ok && p.valid(sv) {
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// withoutConfigSecrets is a copy of values with every secret left out.
func withoutConfigSecrets(n *configNode, values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		p := n.Props[k]
		switch {
		case p == nil:
		case p.Secret:
		case p.Type == "object":
			sub, _ := v.(map[string]any)
			out[k] = withoutConfigSecrets(p, sub)
		default:
			out[k] = v
		}
	}
	return out
}
