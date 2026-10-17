package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"
)

// A blank window is a web napp window with nothing in it, opened by the
// launcher only to fire an action from: the handler picker then covers that
// window like it covers any napp that fires an action, instead of showing up
// in the launcher. It closes itself as soon as the action is routed (or the
// user cancels the picker), and is never listed for reopening.

const blankNappID = "blank"

// blankPage registers a do-nothing action once the bridge is up, which is how
// the launcher knows the page is there to draw a prompt over.
const blankPage = `<!doctype html>
<html><head><meta charset="utf-8"><title></title>
<style>html,body{margin:0;height:100%;background:var(--surface,transparent)}</style>
</head><body><script>
(function ready() {
  if (window.napp && window.napp.registerAction) window.napp.registerAction("` + blankReadyAction + `")
  else setTimeout(ready, 20)
})()
</script></body></html>`

const blankReadyAction = "blank:ready"

// FireFromBlankWindow opens a blank window and fires the action from it. It
// returns right away; failures show up as the launcher's error.
func FireFromBlankWindow(action string, payload json.RawMessage) {
	go func() {
		if err := fireFromBlankWindow(action, payload); err != nil {
			log.Warn().Err(err).Str("action", action).Msg("blank window action failed")
			SetFetchErr(action + ": " + err.Error())
		}
	}()
}

func fireFromBlankWindow(action string, payload json.RawMessage) error {
	dir := nappBaseDir(blankNappID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(blankPage), 0644); err != nil {
		return err
	}

	ci, err := launch(context.Background(), Napp{ID: blankNappID, Name: action})
	if err != nil {
		return err
	}
	ci.blank = true
	ci.auxiliary = true // keeps it off the reopen list once closed
	var closeOnce sync.Once
	closeBlank := func() { closeOnce.Do(ci.Close) }

	waitCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	_, ok := ci.waitForHandler(waitCtx, blankReadyAction)
	cancel()
	if !ok {
		closeBlank()
		return errors.New("the blank window never came up")
	}

	// the dispatch outlives the blank window: it is closed the moment the
	// action is routed, and the handler may take its time to answer
	_, err = runNappAction(context.Background(), ci, action, payload, actionOptions{
		onRouted: closeBlank,
	})
	closeBlank()
	return err
}

// ViewProfile fires view:0 with the pubkey's profile event from a blank
// window, so the user picks which napp shows it.
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
		FireFromBlankWindow("view:0", payload)
	}()
}

// ViewNappEvent fires view:<kind> with the napp's own event from a blank
// window.
func ViewNappEvent(n Napp) {
	kind := n.KindOf()
	payload, _ := json.Marshal(nip19.EncodeNaddr(n.Author, kind, n.D, nil))
	FireFromBlankWindow("view:"+strconv.Itoa(int(kind)), payload)
}
