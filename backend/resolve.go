package backend

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
)

// What a pasted nostr code (or a nostr: link) is opened as.
const (
	OpenedProfile = "profile" // Pubkey: the launcher shows that user's page
	OpenedNapp    = "napp"    // Napp: the launcher shows that napp's page
	OpenedAction  = "action"  // nothing to show: a view:<kind> action was fired
)

// Opened is what OpenCode made of a code.
type Opened struct {
	Kind   string `json:"kind"`
	Pubkey string `json:"pubkey,omitempty"`
	Napp   *Napp  `json:"napp,omitempty"`
}

// OpenCode takes whatever the user pasted — an npub, nprofile, note, nevent
// or naddr, bare or as a nostr: link — and says what to do with it: a user
// goes to their page, a napp to its page, and any other event is handed to
// the napps that view its kind with a view:<kind> action, fired right here.
// It blocks while fetching events, so call it off the render loop.
func OpenCode(ctx context.Context, input string) (Opened, error) {
	code := strings.TrimSpace(input)
	if i := strings.Index(strings.ToLower(code), "nostr:"); i >= 0 {
		code = code[i+len("nostr:"):]
	}
	code = strings.TrimRight(code, "/")
	if code == "" {
		return Opened{}, errors.New("nothing to open")
	}

	prefix, data, err := nip19.Decode(code)
	if err != nil {
		return Opened{}, errors.New("not an npub, nprofile, note, nevent or naddr")
	}

	switch prefix {
	case "npub":
		return Opened{Kind: OpenedProfile, Pubkey: data.(nostr.PubKey).Hex()}, nil
	case "nprofile":
		return Opened{Kind: OpenedProfile, Pubkey: data.(nostr.ProfilePointer).PublicKey.Hex()}, nil
	case "nsec":
		return Opened{}, errors.New("that is a secret key: never paste it anywhere")
	case "naddr":
		ptr := data.(nostr.EntityPointer)
		if !isNappKind(ptr.Kind) {
			// the action resolves the address itself
			return viewCode(ptr.Kind, code)
		}
	case "nevent":
		ptr := data.(nostr.EventPointer)
		if ptr.Kind != 0 && !isNappKind(ptr.Kind) {
			return viewCode(ptr.Kind, code)
		}
	case "note":
	default:
		return Opened{}, errors.New("can't open a " + prefix)
	}

	// an event of a kind we don't know yet, or a napp: get it
	if sys == nil {
		return Opened{}, errors.New("not ready yet")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEventFromInput(fetchCtx, code,
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
	if err != nil || evt == nil {
		return Opened{}, errors.New("couldn't find that event")
	}

	switch {
	case isNappKind(evt.Kind):
		n := nappFromEvent(*evt)
		if n.AuthorName == "" {
			n.AuthorName = n.AuthorShortName()
		}
		rememberNapp(n)
		return Opened{Kind: OpenedNapp, Napp: &n}, nil
	case evt.Kind == 0:
		return Opened{Kind: OpenedProfile, Pubkey: evt.PubKey.Hex()}, nil
	default:
		payload, _ := json.Marshal(evt)
		FireAction("view:"+strconv.Itoa(int(evt.Kind)), payload)
		return Opened{Kind: OpenedAction}, nil
	}
}

func isNappKind(k nostr.Kind) bool { return k == KindWeb || k == KindWasm }

// viewCode fires view:<kind> with the code itself as the payload: the
// handler is given the event once the dispatch resolves it.
func viewCode(kind nostr.Kind, code string) (Opened, error) {
	payload, _ := json.Marshal(code)
	FireAction("view:"+strconv.Itoa(int(kind)), payload)
	return Opened{Kind: OpenedAction}, nil
}

// ─── napps seen outside discovery ───────────────────────────────

// seenNapps are napps the launcher came across outside the discovery list —
// opened from a code, listed on an author's page — so they can still be
// looked up by id to install, preview or draw an icon for.
var seenNapps sync.Map // id -> Napp

func rememberNapp(n Napp) {
	if n.ID == "" {
		return
	}
	if prev, ok := seenNapps.Load(n.ID); ok && prev.(Napp).CreatedAt > n.CreatedAt {
		return
	}
	seenNapps.Store(n.ID, n)
}

func seenNapp(id string) (Napp, bool) {
	if v, ok := seenNapps.Load(id); ok {
		return v.(Napp), true
	}
	return Napp{}, false
}
