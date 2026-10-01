package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// The small NAP domains: theme, storage and link.

func init() {
	handleNap(map[string]napHandler{
		"theme.get": napThemeGet,

		"storage.get":    napStorageGet,
		"storage.set":    napStorageSet,
		"storage.remove": napStorageRemove,
		"storage.keys":   napStorageKeys,

		"link.open": napLinkOpen,
	})
}

// ─── theme ───────────────────────────────────────────────────────

// nappletTheme is the launcher's theme in NAP-THEME's shape: the three
// colors every napplet can count on, from the same tokens napps get as CSS
// variables.
func nappletTheme() map[string]any {
	name, varsJSON := Theme()
	vars := map[string]string{}
	_ = json.Unmarshal([]byte(varsJSON), &vars)
	pick := func(key, fallback string) string {
		if v := vars[key]; v != "" {
			return v
		}
		return fallback
	}
	bg, fg := "#ffffff", "#111111"
	if name == "dark" {
		bg, fg = "#16181d", "#e8e8e8"
	}
	return map[string]any{
		"colors": map[string]any{
			"background": pick("surface", bg),
			"text":       pick("text", fg),
			"primary":    pick("accent", fg),
		},
	}
}

func napThemeGet(c *napCall) {
	c.reply(map[string]any{"theme": nappletTheme()})
}

// broadcastNappletTheme tells every napplet the theme changed.
func broadcastNappletTheme() {
	theme := nappletTheme()
	for _, ci := range allInstances() {
		if ci.nap != nil {
			ci.napPush(map[string]any{"type": "theme.changed", "theme": theme})
		}
	}
}

// ─── storage ─────────────────────────────────────────────────────

// nappletStorageQuota is NAP-STORAGE's recommended per-napplet budget.
const nappletStorageQuota = 512 * 1024

var errNappletQuota = errors.New("quota exceeded")

// napStoreID is which store a storage call goes to. Shared storage belongs to
// the napplet's address (its id), not to one build of it: an update keeps
// the user's data. (NAP-STORAGE scopes by the artifact hash as well; the
// launcher deliberately does not, so a napplet update is not a data wipe.)
// Instance storage belongs to the window.
func napStoreID(c *napCall, scope string) string {
	if scope == "instance" {
		return c.ci.napp.ID + "#" + c.ci.instance
	}
	return c.ci.napp.ID
}

type napStorageReq struct {
	Key   *string `json:"key"`
	Value *string `json:"value"`
	Scope string  `json:"scope"`
}

func (c *napCall) storageReq() (napStorageReq, bool) {
	var r napStorageReq
	if err := c.decode(&r); err != nil {
		c.reply(map[string]any{"error": "invalid request"})
		return r, false
	}
	if r.Scope != "" && r.Scope != "shared" && r.Scope != "instance" {
		c.reply(map[string]any{"error": "invalid scope"})
		return r, false
	}
	return r, true
}

func napStorageGet(c *napCall) {
	r, ok := c.storageReq()
	if !ok {
		return
	}
	if r.Key == nil {
		c.reply(map[string]any{"error": "missing key"})
		return
	}
	if v, found := storageGet(napStoreID(c, r.Scope), *r.Key); found {
		c.reply(map[string]any{"value": v})
		return
	}
	// null, explicitly: the shim hands msg.value straight to the napplet
	c.reply(map[string]any{"value": nil})
}

func napStorageSet(c *napCall) {
	r, ok := c.storageReq()
	if !ok {
		return
	}
	if r.Key == nil || r.Value == nil {
		c.reply(map[string]any{"error": "missing key or value"})
		return
	}
	if err := storageSetQuota(napStoreID(c, r.Scope), *r.Key, *r.Value, nappletStorageQuota, errNappletQuota); err != nil {
		c.reply(map[string]any{"error": err.Error()})
		return
	}
	c.reply(nil)
}

func napStorageRemove(c *napCall) {
	r, ok := c.storageReq()
	if !ok {
		return
	}
	if r.Key == nil {
		c.reply(map[string]any{"error": "missing key"})
		return
	}
	storageRemove(napStoreID(c, r.Scope), *r.Key)
	c.reply(nil)
}

func napStorageKeys(c *napCall) {
	r, ok := c.storageReq()
	if !ok {
		return
	}
	c.reply(map[string]any{"keys": storageKeys(napStoreID(c, r.Scope))})
}

// ─── link ────────────────────────────────────────────────────────

func napLinkOpen(c *napCall) {
	var r struct {
		URL string `json:"url"`
	}
	if err := c.decode(&r); err != nil || strings.TrimSpace(r.URL) == "" {
		c.reply(map[string]any{"error": "invalid-url"})
		return
	}
	u, err := url.Parse(strings.TrimSpace(r.URL))
	if err != nil {
		c.reply(map[string]any{"error": "invalid-url"})
		return
	}
	// javascript:, data:, blob:, file: and every other scheme never leave
	if u.Scheme != "https" && u.Scheme != "http" {
		c.reply(map[string]any{"error": "unsupported-scheme"})
		return
	}
	if u.Host == "" {
		c.reply(map[string]any{"error": "invalid-url"})
		return
	}
	link := u.String()
	c.async(func(context.Context) {
		if !askApproval(c.ci, PermOpenLink, "open a link in your browser", "", preview(link, 200)) {
			c.reply(map[string]any{"status": "denied"})
			return
		}
		if err := openExternalLink(link); err != nil {
			c.reply(map[string]any{"error": err.Error()})
			return
		}
		c.reply(map[string]any{"status": "opened"})
	})
}
