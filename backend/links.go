package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
	"fiatjaf.com/nostr/sdk"
)

// Links: the URLs other programs hand the launcher to open.
//
//   - nostr:<npub|nprofile|nevent|naddr|note> fires "profile" with the hex
//     pubkey for a user, and view:<kind> with the event for anything else
//     ("view" with the code itself when the event can't be found).
//   - https://nostrapps.com/#space=…&app=<napp>&action=<name>~<payload>…
//     previews each napp named and fires the actions that follow it to it,
//     in order.
//   - https://nostrapps.com/#action=<name>~<payload> fires the action with
//     the usual routing, for the user to pick the napp that takes it.

// nostrAppsHost is the site whose links name napps and actions to fire.
const nostrAppsHost = "nostrapps.com"

// IsLink says whether OpenLink knows what to do with this url.
func IsLink(link string) bool {
	link = strings.TrimSpace(link)
	if strings.HasPrefix(strings.ToLower(link), "nostr:") {
		return true
	}
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") &&
		strings.TrimPrefix(strings.ToLower(u.Host), "www.") == nostrAppsHost
}

// OpenLink opens one of the links above. It blocks while fetching events and
// downloading napps, so call it off the render loop.
func OpenLink(link string) error {
	link = strings.TrimSpace(link)
	if strings.HasPrefix(strings.ToLower(link), "nostr:") {
		return openNostrURI(link[len("nostr:"):])
	}
	u, err := url.Parse(link)
	if err != nil {
		return err
	}
	if strings.TrimPrefix(strings.ToLower(u.Host), "www.") != nostrAppsHost {
		return errors.New("not a link verdana opens")
	}
	return openNostrAppsLink(u.Fragment)
}

func openNostrURI(code string) error {
	code = strings.TrimRight(strings.TrimPrefix(code, "//"), "/")
	prefix, _, err := nip19.Decode(code)
	if err != nil {
		return errors.New("not an npub, nprofile, note, nevent or naddr")
	}
	switch prefix {
	case "npub", "nprofile":
		payload, _ := json.Marshal(code)
		FireAction("profile", payload) // goes out as the hex pubkey
	case "nevent", "naddr", "note":
		go func() {
			payload, _ := json.Marshal(code)
			act := resolveView(context.Background(), payload)
			FireAction(act.Type, act.Payload)
		}()
	case "nsec":
		return errors.New("that is a secret key: never paste it anywhere")
	default:
		return errors.New("can't open a " + prefix)
	}
	return nil
}

// linkEntry is one napp of a nostrapps.com link and the actions after it.
type linkEntry struct {
	ref     string
	actions []ShortcutAction
}

// openNostrAppsLink reads the fragment's params in order: each app= starts a
// napp, each action= goes to the napp before it, or to whoever the user
// picks when no napp came before it.
func openNostrAppsLink(fragment string) error {
	var entries []linkEntry
	var loose []ShortcutAction
	for _, param := range strings.Split(fragment, "&") {
		key, value, _ := strings.Cut(param, "=")
		if v, err := url.QueryUnescape(value); err == nil {
			value = v
		}
		switch key {
		case "app":
			if value != "" {
				entries = append(entries, linkEntry{ref: strings.TrimPrefix(value, "nostr:")})
			}
		case "action":
			act, ok := parseLinkAction(value)
			if !ok {
				continue
			}
			if len(entries) == 0 {
				loose = append(loose, act)
			} else {
				entries[len(entries)-1].actions = append(entries[len(entries)-1].actions, act)
			}
		}
	}
	if len(entries) == 0 && len(loose) == 0 {
		return errors.New("that link names no napp and no action")
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(),
			shortcutActionTimeout*time.Duration(len(entries)+len(loose)+1))
		defer cancel()
		waitStartupLogin(ctx)

		for _, act := range loose {
			act = resolveLinkAction(ctx, act)
			if _, err := runNappAction(ctx, nil, act.Type, act.Payload, actionOptions{}); err != nil {
				log.Warn().Err(err).Str("action", act.Type).Msg("link action failed")
				SetFetchErr(act.Type + ": " + err.Error())
			}
		}
		for _, e := range entries {
			if err := previewAndDispatch(ctx, e); err != nil {
				log.Warn().Err(err).Str("napp", e.ref).Msg("link napp failed")
				SetFetchErr("link failed: " + err.Error())
			}
		}
	}()
	return nil
}

// parseLinkAction reads <name>~<payload>: the payload is JSON when it parses
// as such (an event, an object, a quoted string) and a plain string
// otherwise (an npub, an nevent, a url).
func parseLinkAction(value string) (ShortcutAction, bool) {
	name, raw, _ := strings.Cut(value, "~")
	if validActionName(name) != nil {
		return ShortcutAction{}, false
	}
	act := ShortcutAction{Type: name}
	if raw == "" {
		return act, true
	}
	if json.Valid([]byte(raw)) && strings.ContainsAny(raw[:1], `{["`) {
		act.Payload = json.RawMessage(raw)
	} else {
		act.Payload, _ = json.Marshal(raw)
	}
	return act, true
}

// resolveLinkAction turns a link's "view" into view:<kind> when its event can
// be found.
func resolveLinkAction(ctx context.Context, act ShortcutAction) ShortcutAction {
	if act.Type != "view" {
		return act
	}
	return resolveView(ctx, act.Payload)
}

// resolveView is what a "view" of this payload (an event, or a code
// pointing at one) goes out as: view:<kind> with the event when it can be
// had, a plain "view" with the payload as given otherwise.
func resolveView(ctx context.Context, payload json.RawMessage) ShortcutAction {
	resolved, ok := resolveViewPayload(ctx, payload)
	if ok {
		var evt struct {
			Kind *nostr.Kind `json:"kind"`
		}
		if err := json.Unmarshal(resolved, &evt); err == nil && evt.Kind != nil {
			return ShortcutAction{Type: "view:" + strconv.Itoa(int(*evt.Kind)), Payload: resolved}
		}
	}
	return ShortcutAction{Type: "view", Payload: payload}
}

// previewAndDispatch finds the napp a link entry names, opens it (as a
// preview when it isn't on disk yet) and fires its actions to it in order.
func previewAndDispatch(ctx context.Context, e linkEntry) error {
	n, err := nappFromRef(ctx, e.ref)
	if err != nil {
		return err
	}
	if devLookup(n.ID) == nil {
		if local, ok := InstalledNapp(n.ID); ok {
			n = local
		} else if err := downloadPreview(ctx, n); err != nil {
			return err
		}
	}

	actions := make([]ShortcutAction, len(e.actions))
	for i, act := range e.actions {
		actions[i] = resolveLinkAction(ctx, act)
	}
	return openAndDispatch(ctx, n, actions)
}

// nappFromRef fetches the napp an naddr or nevent points at.
func nappFromRef(ctx context.Context, ref string) (Napp, error) {
	if sys == nil {
		return Napp{}, errors.New("not ready yet")
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	evt, _, err := sys.FetchSpecificEventFromInput(fetchCtx, ref,
		sdk.FetchSpecificEventParameters{SaveToLocalStore: true})
	if err != nil || evt == nil {
		return Napp{}, errors.New("couldn't find napp " + ref)
	}
	if !isNappKind(evt.Kind) {
		return Napp{}, errors.New(ref + " is no napp")
	}
	n := nappFromEvent(*evt)
	if n.AuthorName == "" {
		n.AuthorName = n.AuthorShortName()
	}
	rememberNapp(n)
	return n, nil
}

// normalizePayload makes a payload that is an npub or nprofile (a JSON
// string, maybe a nostr: link) the hex pubkey it stands for: napps are only
// ever handed hex pubkeys, whoever fired the action and however they wrote
// it. A "view" is left alone, as its string payload is a pointer to an
// event, which a bare pubkey would not be.
func normalizePayload(action string, payload json.RawMessage) json.RawMessage {
	if action == "view" || strings.HasPrefix(action, "view:") {
		return payload
	}
	var s string
	if err := json.Unmarshal(payload, &s); err != nil {
		return payload
	}
	code := strings.TrimSpace(s)
	if len(code) > 6 && strings.EqualFold(code[:6], "nostr:") {
		code = code[6:]
	}
	if !strings.HasPrefix(code, "npub1") && !strings.HasPrefix(code, "nprofile1") {
		return payload
	}
	var pk nostr.PubKey
	switch prefix, data, err := nip19.Decode(code); {
	case err != nil:
		return payload
	case prefix == "npub":
		pk = data.(nostr.PubKey)
	case prefix == "nprofile":
		pk = data.(nostr.ProfilePointer).PublicKey
	default:
		return payload
	}
	out, _ := json.Marshal(pk.Hex())
	return out
}
