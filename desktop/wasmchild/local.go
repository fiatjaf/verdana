package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// ─── localStorage ────────────────────────────────────────────────

// storageQuota is the same 5MB bridge.js gives a web napp.
const storageQuota = 5 * 1024 * 1024

// store is this window's copy of the napp's storage: seeded from the file
// the launcher persists, written through on every change, and kept in step
// with the napp's other windows by the launcher's storage messages.
type store struct {
	mu   sync.Mutex
	m    map[string]string
	size int
}

func loadStore(path string) *store {
	s := &store{m: make(map[string]string)}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var seed map[string]string
	if json.Unmarshal(raw, &seed) != nil {
		return s
	}
	for k, v := range seed {
		if s.size+len(k)+len(v) > storageQuota {
			break
		}
		s.m[k] = v
		s.size += len(k) + len(v)
	}
	return s
}

func (s *store) get(k string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	return v, ok
}

func (s *store) keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (s *store) set(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delta := len(k) + len(v)
	if old, ok := s.m[k]; ok {
		delta -= len(k) + len(old)
	}
	if s.size+delta > storageQuota {
		return errors.New("storage quota exceeded (5MB)")
	}
	s.m[k] = v
	s.size += delta
	return nil
}

func (s *store) remove(k string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.m[k]; ok {
		s.size -= len(k) + len(old)
		delete(s.m, k)
	}
}

func (s *store) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = make(map[string]string)
	s.size = 0
}

// apply is a change another window made. The quota is enforced where the
// write happened; a synced change always lands so every window converges on
// what the launcher has.
func (s *store) apply(op, k, v string) {
	switch op {
	case "set":
		s.mu.Lock()
		if old, ok := s.m[k]; ok {
			s.size -= len(k) + len(old)
		}
		s.m[k] = v
		s.size += len(k) + len(v)
		s.mu.Unlock()
	case "remove":
		s.remove(k)
	case "clear":
		s.clear()
	}
}

// ─── pure helpers ────────────────────────────────────────────────
//
// window.napp.nip19 and window.napp.fx run inside the page, synchronously,
// in bridge.js. These are the same functions, answering napp_call under the
// names "nip19.<fn>" and "fx.<fn>". A function of one argument takes that
// argument as its params; one taking a pointer takes the pointer object.
//
// The nip19 and coordinate logic comes from fiatjaf.com/nostr; this file
// only adapts its types to the {type, data} shapes bridge.js produces. Only
// the bolt11 amount-prefix sniff is hand-rolled.

var pureMethods = map[string]func(json.RawMessage) (any, error){
	"nip19.decode": func(p json.RawMessage) (any, error) {
		s, err := stringParam(p)
		if err != nil {
			return nil, err
		}
		return nip19Decode(s)
	},
	"nip19.npubEncode": func(p json.RawMessage) (any, error) {
		pk, err := pubKeyParam(p)
		if err != nil {
			return nil, err
		}
		return nip19.EncodeNpub(pk), nil
	},
	// noteEncode is an alias for neventEncode({id}): napps get nevents.
	"nip19.noteEncode": func(p json.RawMessage) (any, error) {
		id, err := idParam(p)
		if err != nil {
			return nil, err
		}
		return nip19.EncodeNevent(id, nil, nostr.ZeroPK), nil
	},
	"nip19.neventEncode": func(p json.RawMessage) (any, error) {
		var ptr struct {
			ID     string   `json:"id"`
			Relays []string `json:"relays"`
			Author string   `json:"author"`
			Kind   *uint32  `json:"kind"`
		}
		if err := json.Unmarshal(p, &ptr); err != nil {
			return nil, err
		}
		id, err := hexID(ptr.ID)
		if err != nil {
			return nil, err
		}
		var author nostr.PubKey
		if ptr.Author != "" {
			if author, err = hexPubKey(ptr.Author); err != nil {
				return nil, err
			}
		}
		if ptr.Kind == nil {
			return nip19.EncodeNevent(id, ptr.Relays, author), nil
		}
		return nip19.EncodeNeventWithKind(id, ptr.Relays, author, nostr.Kind(*ptr.Kind)), nil
	},
	"nip19.naddrEncode": func(p json.RawMessage) (any, error) {
		var ptr struct {
			Identifier string   `json:"identifier"`
			Pubkey     string   `json:"pubkey"`
			Kind       uint32   `json:"kind"`
			Relays     []string `json:"relays"`
		}
		if err := json.Unmarshal(p, &ptr); err != nil {
			return nil, err
		}
		pk, err := hexPubKey(ptr.Pubkey)
		if err != nil {
			return nil, err
		}
		if ptr.Kind > math.MaxUint16 {
			return nil, errors.New("invalid kind")
		}
		return nip19.EncodeNaddr(pk, nostr.Kind(ptr.Kind), ptr.Identifier, ptr.Relays), nil
	},

	"fx.isHex64": func(p json.RawMessage) (any, error) {
		s, err := stringParam(p)
		// IsValid32ByteHex wants lowercase; bridge.js matches either case.
		return err == nil && nostr.IsValid32ByteHex(strings.ToLower(s)), nil
	},
	"fx.parseCoordinate": func(p json.RawMessage) (any, error) {
		s, err := stringParam(p)
		if err != nil {
			return nil, nil
		}
		return parseCoordinate(s), nil
	},
	"fx.formatCoordinate": func(p json.RawMessage) (any, error) {
		var c coordinate
		if err := json.Unmarshal(p, &c); err != nil {
			return nil, err
		}
		return strconv.Itoa(c.Kind) + ":" + c.Pubkey + ":" + c.Identifier, nil
	},
	"fx.satsFromBolt11": func(p json.RawMessage) (any, error) {
		s, err := stringParam(p)
		if err != nil {
			return nil, nil
		}
		return satsFromBolt11(s), nil
	},
}

func stringParam(p json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(p, &s); err != nil {
		return "", errors.New("params must be a string")
	}
	return s, nil
}

func hexBytes(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("invalid hex")
	}
	return hex.DecodeString(s)
}

// hexID and hexPubKey are hexBytes requiring exactly 32 bytes, without the
// curve checks of PubKeyFromHex: like bridge.js, any 64 hex chars encode.
func hexID(s string) (nostr.ID, error) {
	b, err := hexBytes(s)
	if err != nil || len(b) != 32 {
		return nostr.ZeroID, errors.New("invalid hex")
	}
	var id nostr.ID
	copy(id[:], b)
	return id, nil
}

func hexPubKey(s string) (nostr.PubKey, error) {
	b, err := hexBytes(s)
	if err != nil || len(b) != 32 {
		return nostr.ZeroPK, errors.New("invalid hex")
	}
	var pk nostr.PubKey
	copy(pk[:], b)
	return pk, nil
}

func idParam(p json.RawMessage) (nostr.ID, error) {
	s, err := stringParam(p)
	if err != nil {
		return nostr.ZeroID, err
	}
	return hexID(s)
}

func pubKeyParam(p json.RawMessage) (nostr.PubKey, error) {
	s, err := stringParam(p)
	if err != nil {
		return nostr.ZeroPK, err
	}
	return hexPubKey(s)
}

func relaysOrEmpty(r []string) []string {
	if r == nil {
		return []string{}
	}
	return r
}

// nip19Decode is bridge.js's nip19Decode: the same {type, data} shapes,
// decoded by the nostr library, with its errors translated back to the
// messages bridge.js throws so napps see the same strings on both runtimes.
func nip19Decode(code string) (any, error) {
	prefix, value, err := nip19.Decode(code)
	if err != nil {
		return nil, decodeError(prefix, err)
	}
	type result struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}

	switch prefix {
	case "npub":
		return result{prefix, value.(nostr.PubKey).Hex()}, nil
	case "note":
		return result{prefix, value.(nostr.EventPointer).ID.Hex()}, nil
	case "nsec":
		return result{prefix, value.(nostr.SecretKey).Hex()}, nil
	case "nprofile":
		pp := value.(nostr.ProfilePointer)
		return result{prefix, map[string]any{
			"pubkey": pp.PublicKey.Hex(),
			"relays": relaysOrEmpty(pp.Relays),
		}}, nil
	case "nevent":
		ep := value.(nostr.EventPointer)
		d := map[string]any{
			"id":     ep.ID.Hex(),
			"relays": relaysOrEmpty(ep.Relays),
		}
		if ep.Author != nostr.ZeroPK {
			d["author"] = ep.Author.Hex()
		}
		if ep.Kind != 0 {
			d["kind"] = uint32(ep.Kind)
		}
		return result{prefix, d}, nil
	case "naddr":
		ap := value.(nostr.EntityPointer)
		return result{prefix, map[string]any{
			"identifier": ap.Identifier,
			"pubkey":     ap.PublicKey.Hex(),
			"kind":       uint32(ap.Kind),
			"relays":     relaysOrEmpty(ap.Relays),
		}}, nil
	}
	return nil, errors.New("unsupported prefix: " + prefix)
}

func decodeError(prefix string, err error) error {
	if prefix == "" {
		if strings.HasPrefix(err.Error(), "failed to translate data into 8 bits") {
			return errors.New("invalid bech32 data")
		}
		return errors.New("invalid bech32")
	}
	switch prefix {
	case "nprofile":
		return errors.New("nprofile missing pubkey")
	case "nevent":
		return errors.New("nevent missing id")
	case "naddr":
		return errors.New("invalid naddr")
	case "npub", "note", "nsec":
		return err
	default:
		return errors.New("unsupported prefix: " + prefix)
	}
}

type coordinate struct {
	Kind       int    `json:"kind"`
	Pubkey     string `json:"pubkey"`
	Identifier string `json:"identifier"`
}

func parseCoordinate(s string) *coordinate {
	ep, err := nostr.ParseAddrString(s)
	if err != nil || ep.Kind > math.MaxUint16 {
		return nil
	}
	return &coordinate{Kind: int(ep.Kind), Pubkey: ep.PublicKey.Hex(), Identifier: ep.Identifier}
}

var bolt11Prefix = regexp.MustCompile(`^ln(?:bc|tbs?|bcrt|sb)(\d*)([munp]?)$`)

// satsFromBolt11 is the invoice's amount in sats, 0 for an amountless one and
// nil for something that is not an invoice.
func satsFromBolt11(invoice string) any {
	s := strings.ToLower(strings.TrimSpace(invoice))
	pos := strings.LastIndex(s, "1")
	if pos < 0 {
		return nil
	}
	m := bolt11Prefix.FindStringSubmatch(s[:pos])
	if m == nil {
		return nil
	}
	if m[1] == "" {
		return 0
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return nil
	}
	factor := map[string]float64{"m": 1e-3, "u": 1e-6, "n": 1e-9, "p": 1e-12}[m[2]]
	if factor == 0 {
		factor = 1
	}
	return int64(math.Round(n * factor * 1e8))
}
