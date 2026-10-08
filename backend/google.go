package backend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/promenade/frost"
	"github.com/btcsuite/btcd/btcec/v2"
	"golang.org/x/crypto/argon2"
)

// "Log in with Google" through Pomegranate: Google vouches for an email at a
// central server, a fresh key is FROST-sharded across a set of operators, and
// what comes out at the end is a plain bunker:// url handed to Login — the
// same thing the user could have pasted, they just never see it.
//
// Central gives its token out through window.opener.postMessage from an OAuth
// popup, which only a browser page can receive. So the launcher serves that
// page itself on a loopback port, opens it in the system browser (where
// Google is happy to log people in, unlike in embedded webviews), and the
// page posts the token back here.
//
// On Android the Google login is native instead (see LoginWithGoogleIDToken).
//
// This mirrors nostrapps' src/signers/google.ts.

// the Pomegranate deployment this launcher points at
const pomegranateCentral = "https://auth.njump.me"

var pomegranateOperators = []string{
	"https://po.njump.me",
	"https://po.nostrver.se",
	"https://po.f7z.io",
	"https://po.jumble.social",
	"https://po.coracle.social",
}

const (
	kindSetupAnnouncement    nostr.Kind = 16440
	kindOperatorRegistration nostr.Kind = 20444
	kindCentralRegistration  nostr.Kind = 20445
)

// where setup announcements are looked up and published
var pomegranateBigRelays = []string{"wss://relay.damus.io", "wss://relay.primal.net", "wss://nos.lol"}

var (
	googleMu      sync.Mutex
	googleCancel  context.CancelFunc
	googleAttempt int
)

// LoginWithGoogle runs the whole Google → Pomegranate → bunker flow, getting
// the Google login through the system browser, and then logs in with the
// bunker. Progress shows up in State.LoginStatus, failures in LoginErr.
// Calling it again abandons an attempt still waiting on the browser.
// Blocking: call it from a goroutine.
func LoginWithGoogle() {
	loginWithCentralToken(func(ctx context.Context) (string, error) {
		setLoginStatus("Waiting for Google login in the browser…")
		return googleToken(ctx)
	})
}

// LoginWithGoogleIDToken is LoginWithGoogle for a platform that did the
// Google login natively (Android's Credential Manager): central trades the
// ID token, issued for its own web client id, for its token. Blocking.
func LoginWithGoogleIDToken(idToken string) {
	loginWithCentralToken(func(ctx context.Context) (string, error) {
		setLoginStatus("Logging in with central…")
		var resp struct {
			Token string `json:"token"`
		}
		if _, err := httpJSON(ctx, "POST", pomegranateCentral+"/login/google/android",
			map[string]string{"id_token": idToken}, nil, &resp); err != nil {
			return "", fmt.Errorf("central google login: %w", err)
		}
		return resp.Token, nil
	})
}

// loginWithCentralToken gets a central token with getToken, turns it into a
// bunker and logs in with that.
func loginWithCentralToken(getToken func(ctx context.Context) (string, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	googleMu.Lock()
	if googleCancel != nil {
		googleCancel()
	}
	googleCancel = cancel
	googleAttempt++
	attempt := googleAttempt
	googleMu.Unlock()
	defer func() {
		cancel()
		googleMu.Lock()
		if googleAttempt == attempt {
			googleCancel = nil
		}
		googleMu.Unlock()
	}()

	uri, err := func() (string, error) {
		token, err := getToken(ctx)
		if err != nil {
			return "", err
		}
		return googleBunker(ctx, token)
	}()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// a newer attempt took over, it owns the status line now
			return
		}
		log.Warn().Err(err).Msg("google login failed")
		setLoginErr(err.Error())
		return
	}

	setLoginStatus("")
	Login(uri)
}

func googleBunker(ctx context.Context, token string) (string, error) {
	status := func(msg string) {
		log.Info().Msg("google login: " + msg)
		setLoginStatus(msg)
	}

	email, err := tokenEmail(token)
	if err != nil {
		return "", err
	}

	pc := pomegranateClient{central: pomegranateCentral, token: token}

	status("Checking account…")
	if _, found, err := pc.account(ctx); err != nil {
		return "", err
	} else if found {
		status("Account found, fetching default bunker…")
		return pc.defaultBunker(ctx, status)
	}

	// no account here, but there may be one at another central: send the
	// user there instead of minting a duplicate key
	if actual := announcedCentral(ctx, email); actual != "" && actual != pomegranateCentral {
		return "", fmt.Errorf("account already set up at %s (this launcher uses %s)", actual, pomegranateCentral)
	}

	// first time: make a key and shard it across the operators
	status("Generating key…")
	sk := nostr.Generate()
	defer func() { sk = nostr.SecretKey{} }()

	operators := pomegranateOperators
	threshold := (len(operators)*7 + 11) / 12 // ceil(n*7/12)

	status("Splitting secret…")
	var secret btcec.ModNScalar
	if overflow := secret.SetByteSlice(sk[:]); overflow {
		return "", errors.New("generated key is out of range")
	}
	shards, _, _ := frost.TrustedKeyDeal(&secret, threshold, len(operators))

	session := nostr.Generate().Hex()

	status("Registering with central…")
	reg := nostr.Event{
		Kind:      kindCentralRegistration,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"central", pomegranateCentral},
			{"threshold", strconv.Itoa(threshold)},
		},
	}
	for i, op := range operators {
		reg.Tags = append(reg.Tags, nostr.Tag{"operator", op, shards[i].PublicKeyShard.Hex()})
	}
	if err := reg.Sign(sk); err != nil {
		return "", err
	}
	if _, err := pc.do(ctx, "POST", "/register", reg,
		map[string]string{"X-Pomegranate-Session": session}, nil); err != nil {
		return "", fmt.Errorf("central /register: %w", err)
	}

	// each operator gets its own shard. If one fails after central is
	// registered the account is half-provisioned and has to be reset.
	for i, op := range operators {
		status("Registering with " + op + "…")
		evt := nostr.Event{
			Kind:      kindOperatorRegistration,
			CreatedAt: nostr.Now(),
			Tags: nostr.Tags{
				{"central", pomegranateCentral},
				{"email", email},
			},
			Content: shards[i].Hex(),
		}
		if err := evt.Sign(sk); err != nil {
			return "", err
		}
		opToken := sha256.Sum256([]byte(session + ":" + op))
		if _, err := httpJSON(ctx, "POST", op+"/po/register", evt, map[string]string{
			"X-Pomegranate-Operator-Token": hex.EncodeToString(opToken[:]),
		}, nil); err != nil {
			return "", fmt.Errorf("%s /po/register: %w", op, err)
		}
	}

	status("Announcing setup…")
	announceSetup(ctx, email, sk)

	// the operators confirm with central in the background
	status("Waiting for operators to confirm…")
	for i := 0; ; i++ {
		if _, found, err := pc.account(ctx); err != nil {
			return "", err
		} else if found {
			break
		}
		if i == 15 {
			return "", errors.New("account creation failed (operators never confirmed)")
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	return pc.defaultBunker(ctx, status)
}

// googleToken serves the loopback page, sends the browser to it and waits
// for the token central hands that page.
func googleToken(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()

	nonce := shortID()
	tokens := make(chan string, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /"+nonce, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		googlePage.Execute(w, map[string]string{"Central": pomegranateCentral, "Nonce": nonce})
	})
	mux.HandleFunc("POST /"+nonce+"/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 64*1024))
		token := strings.TrimSpace(string(body))
		if _, err := tokenEmail(token); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case tokens <- token:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	page := "http://" + ln.Addr().String() + "/" + nonce
	if err := host.OpenLink(page); err != nil {
		return "", fmt.Errorf("could not open the browser: %w", err)
	}

	select {
	case token := <-tokens:
		return token, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("google login timed out")
		}
		return "", ctx.Err()
	}
}

var googlePage = template.Must(template.New("google").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Verdana · Google login</title>
<style>
  body { font-family: system-ui, sans-serif; display: flex; min-height: 100vh; margin: 0;
         align-items: center; justify-content: center; background: #f6f7f5; color: #1d211c; }
  main { max-width: 360px; padding: 16px; text-align: center; }
  button { font: inherit; padding: 10px 20px; border-radius: 8px; border: 1px solid #1d211c33;
           background: #fff; cursor: pointer; }
  #msg { margin-top: 16px; color: #555; }
  @media (prefers-color-scheme: dark) {
    body { background: #161915; color: #e6e9e4; } button { background: #23271f; color: inherit; } #msg { color: #aaa; }
  }
</style></head>
<body><main>
  <h2>Log in to Verdana</h2>
  <button id="go">Continue with Google</button>
  <p id="msg"></p>
</main>
<script>
  const central = {{.Central}}
  const nonce = {{.Nonce}}
  const btn = document.getElementById("go")
  const msg = document.getElementById("msg")
  let popup = null

  function openPopup() {
    popup = window.open(central + "/login/google", "PomegranateOAuth", "width=600,height=600")
    if (!popup) msg.textContent = "The popup was blocked, click the button to try again."
  }
  btn.onclick = openPopup
  openPopup()

  window.addEventListener("message", async event => {
    if (event.origin !== central || event.source !== popup || !event.data?.token) return
    try { popup.close() } catch {}
    btn.hidden = true
    msg.textContent = "Finishing…"
    try {
      const r = await fetch("/" + nonce + "/token", { method: "POST", body: event.data.token })
      if (!r.ok) throw new Error(await r.text())
      msg.textContent = "Done! You can close this tab and go back to Verdana."
    } catch (err) {
      msg.textContent = "Verdana didn't get the login (" + err.message + "). Is it still open?"
      btn.hidden = false
    }
  })
</script>
</body></html>`))

// tokenEmail reads the email out of a central token: a base64 kind 20443
// event with an "email" tag.
func tokenEmail(token string) (string, error) {
	j, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", errors.New("central token is not base64")
	}
	var evt nostr.Event
	if err := json.Unmarshal(j, &evt); err != nil {
		return "", errors.New("central token is not an event")
	}
	if tag := evt.Tags.Find("email"); tag != nil {
		return tag[1], nil
	}
	return "", errors.New("central token has no email")
}

func emailSetupHash(email string) string {
	return hex.EncodeToString(argon2.IDKey([]byte(email), []byte("pomegranate"), 1, 64*1024, 4, 32))
}

// announcedCentral looks up the kind 16440 announcement for this email to
// learn which central it registered at. "" when there is none or the lookup
// fails: GET /account stays authoritative, a relay outage must not block
// logins.
func announcedCentral(ctx context.Context, email string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	re := sys.Pool.QuerySingle(ctx, pomegranateBigRelays, nostr.Filter{
		Kinds: []nostr.Kind{kindSetupAnnouncement},
		Tags:  nostr.TagMap{"m": []string{emailSetupHash(email)}},
		Limit: 1,
	}, nostr.SubscriptionOptions{})
	if re == nil {
		return ""
	}
	if tag := re.Tags.Find("central"); tag != nil {
		return tag[1]
	}
	return ""
}

// announceSetup publishes where this email's setup lives, so other clients
// (and later logins) find the right central. Best-effort.
func announceSetup(ctx context.Context, email string, sk nostr.SecretKey) {
	evt := nostr.Event{
		Kind:      kindSetupAnnouncement,
		CreatedAt: nostr.Now(),
		Tags: nostr.Tags{
			{"m", emailSetupHash(email)},
			{"central", pomegranateCentral},
		},
	}
	if err := evt.Sign(sk); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// a brand-new key has no relay list of its own yet, so only the big relays
	for res := range sys.Pool.PublishMany(ctx, pomegranateBigRelays, evt) {
		if res.Error != nil {
			log.Debug().Err(res.Error).Str("relay", res.RelayURL).Msg("setup announcement not published")
		}
	}
}

// pomegranateClient talks to central with the user's token.
type pomegranateClient struct {
	central string
	token   string
}

func (pc pomegranateClient) do(ctx context.Context, method, path string, body any, headers map[string]string, out any) (int, error) {
	h := map[string]string{"Authorization": "Token " + pc.token}
	for k, v := range headers {
		h[k] = v
	}
	return httpJSON(ctx, method, pc.central+path, body, h, out)
}

// account says whether central has an operational account for the token's
// email (a 404 is "not yet", not an error).
func (pc pomegranateClient) account(ctx context.Context) (json.RawMessage, bool, error) {
	var acct json.RawMessage
	code, err := pc.do(ctx, "GET", "/account", nil, nil, &acct)
	if code == http.StatusNotFound {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("central /account: %w", err)
	}
	return acct, true, nil
}

// defaultBunker finds (or creates) the profile named "default" and builds
// its bunker url: central is also the NIP-46 relay.
func (pc pomegranateClient) defaultBunker(ctx context.Context, status func(string)) (string, error) {
	type profile struct {
		Name          string `json:"name"`
		HandlerPubKey string `json:"handler_pubkey"`
	}
	find := func() (*profile, error) {
		var profiles []profile
		if _, err := pc.do(ctx, "GET", "/profiles", nil, nil, &profiles); err != nil {
			return nil, fmt.Errorf("profile list: %w", err)
		}
		for _, p := range profiles {
			if p.Name == "default" {
				return &p, nil
			}
		}
		return nil, nil
	}

	p, err := find()
	if err != nil {
		return "", err
	}
	if p == nil {
		status("Creating default signing profile…")
		if _, err := pc.do(ctx, "POST", "/profiles", map[string]string{"name": "default"}, nil, nil); err != nil {
			return "", fmt.Errorf("profile creation: %w", err)
		}
		if p, err = find(); err != nil {
			return "", err
		}
	}
	if p == nil || p.HandlerPubKey == "" {
		return "", errors.New(`central did not return a "default" profile after creation`)
	}

	relay := "ws" + strings.TrimPrefix(pc.central, "http")
	return "bunker://" + p.HandlerPubKey + "?relay=" + url.QueryEscape(relay), nil
}

// httpJSON sends body as JSON (when there is one) and decodes the answer
// into out (when asked to). Non-2xx answers are errors carrying the start of
// whatever the server said, and the status code is returned either way.
func httpJSON(ctx context.Context, method, u string, body any, headers map[string]string, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		j, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(j)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			return resp.StatusCode, fmt.Errorf("failed (%d): %s", resp.StatusCode, msg)
		}
		return resp.StatusCode, fmt.Errorf("failed (%d)", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("unreadable answer: %w", err)
		}
	}
	return resp.StatusCode, nil
}
