package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"unicode"
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

const maxNapLinkLabelRunes = 200

type napLinkOpenReq struct {
	URL     string `json:"url"`
	Options struct {
		Label string `json:"label"`
	} `json:"options"`
}

func napLinkDenied(c *napCall, code string) {
	c.reply(map[string]any{"status": "denied", "error": code})
}

// napLinkLabel makes untrusted, optional prompt text display-safe. It remains
// supplementary: the actual normalized URL is always the prompt's code field.
func napLinkLabel(label string) string {
	label = strings.Map(func(r rune) rune {
		// Format controls include bidirectional overrides that could make the
		// untrusted label visually disagree with the URL shown below it.
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, label)
	label = strings.Join(strings.Fields(label), " ")
	runes := []rune(label)
	if len(runes) > maxNapLinkLabelRunes {
		label = string(runes[:maxNapLinkLabelRunes]) + "…"
	}
	return label
}

func napLinkOpen(c *napCall) {
	var r napLinkOpenReq
	if err := c.decode(&r); err != nil || strings.TrimSpace(r.URL) == "" {
		napLinkDenied(c, "invalid-url")
		return
	}
	u, err := url.Parse(strings.TrimSpace(r.URL))
	if err != nil {
		napLinkDenied(c, "invalid-url")
		return
	}
	if u.Scheme == "" {
		napLinkDenied(c, "invalid-url")
		return
	}
	// javascript:, data:, blob:, file: and every other scheme never leave
	if u.Scheme != "https" && u.Scheme != "http" {
		napLinkDenied(c, "unsupported-scheme")
		return
	}
	if u.Host == "" {
		napLinkDenied(c, "invalid-url")
		return
	}
	link := u.String()
	detail := ""
	if label := napLinkLabel(r.Options.Label); label != "" {
		detail = "The napplet describes this link as: " + label
	}
	c.async(func(context.Context) {
		if !askApproval(c.ci, PermOpenLink, "open a link in your browser", detail, preview(link, 200)) {
			napLinkDenied(c, "user-denied")
			return
		}
		if err := openExternalLink(link); err != nil {
			log.Warn().Err(err).Str("url", link).Msg("approved NAP-LINK request could not be opened")
			napLinkDenied(c, "blocked-by-policy")
			return
		}
		c.reply(map[string]any{"status": "opened"})
	})
}
