package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

// NAP-MEDIA: media sessions. A shell-owned session plays its source in the
// platform's media player (mpv or VLC on the desktop, another app on
// Android) and reports that player's state back; the napplet never touches
// the bytes. A napplet-owned session is the napplet's own playback, which
// the launcher only keeps track of: it has no media controls of its own yet.

func init() {
	handleNap(map[string]napHandler{
		"media.session.create":  napMediaCreate,
		"media.session.update":  napMediaUpdate,
		"media.session.destroy": napMediaDestroy,
		"media.state":           napMediaState,
		"media.capabilities":    napMediaCapabilities,
		"media.command":         napMediaCommand,
	})
}

const (
	// mediaMaxSessions caps the sessions one napplet window has open.
	mediaMaxSessions = 4
	// mediaStateInterval spaces out media.state pushes that only move the
	// position: players report it many times a second.
	mediaStateInterval = time.Second
)

var mediaActions = []string{"play", "pause", "stop", "next", "prev", "seek", "volume"}

var mediaStatuses = []string{"playing", "paused", "stopped", "buffering"}

// mediaShellActions are what a shell-owned session can be told to do; a live
// source can't seek.
func mediaShellActions(live bool) []string {
	if live {
		return []string{"play", "pause", "stop", "volume"}
	}
	return []string{"play", "pause", "stop", "seek", "volume"}
}

type mediaSession struct {
	id       string
	owner    string
	live     bool
	metadata map[string]any
	actions  []string

	// player is a shell-owned session's playback, nil until it starts
	player MediaPlayer
	// last is the newest state the player reported; pushed and pushedAt
	// are the last one the napplet was sent
	last     *MediaState
	pushed   *MediaState
	pushedAt time.Time

	// state is what a napplet-owned session last reported
	state map[string]any
}

func (ms *mediaSession) stop() {
	if ms.player != nil {
		if err := ms.player.Stop(); err != nil {
			log.Debug().Err(err).Str("session", ms.id).Msg("media player did not stop")
		}
	}
}

type mediaSourceRef struct {
	URL         string          `json:"url"`
	BlossomHash string          `json:"blossomHash"`
	Nostr       json.RawMessage `json:"nostr"`
	MimeType    string          `json:"mimeType"`
}

type mediaCreateReq struct {
	Owner        string          `json:"owner"`
	SessionID    string          `json:"sessionId"`
	Source       *mediaSourceRef `json:"source"`
	Metadata     map[string]any  `json:"metadata"`
	Capabilities []string        `json:"capabilities"`
	Autoplay     bool            `json:"autoplay"`
	Live         bool            `json:"live"`
}

func napMediaCreate(c *napCall) {
	fail := func(msg string) { c.reply(map[string]any{"error": msg}) }
	var r mediaCreateReq
	if err := c.decode(&r); err != nil {
		fail("invalid request")
		return
	}
	switch r.Owner {
	case "shell", "napplet":
	case "":
		fail("missing owner")
		return
	default:
		fail("unsupported owner mode")
		return
	}
	if r.Owner == "shell" && r.Source == nil {
		fail("missing source")
		return
	}

	s := c.ci.nap
	s.mu.Lock()
	if s.gen != c.gen {
		s.mu.Unlock()
		return
	}
	if len(s.media) >= mediaMaxSessions {
		s.mu.Unlock()
		fail("session limit exceeded")
		return
	}
	s.mediaSeq++
	ms := &mediaSession{
		id:       fmt.Sprintf("%s-%d", r.Owner, s.mediaSeq),
		owner:    r.Owner,
		live:     r.Live,
		metadata: r.Metadata,
	}
	if ms.metadata == nil {
		ms.metadata = map[string]any{}
	}
	if r.Owner == "napplet" {
		ms.actions = mediaActionList(r.Capabilities)
	} else {
		ms.actions = mediaShellActions(r.Live)
	}
	s.media[ms.id] = ms
	s.mu.Unlock()

	if r.Owner == "napplet" {
		c.reply(map[string]any{"sessionId": ms.id, "owner": ms.owner})
		return
	}

	drop := func() {
		s.mu.Lock()
		if s.media[ms.id] == ms {
			delete(s.media, ms.id)
		}
		s.mu.Unlock()
	}
	c.async(func(ctx context.Context) {
		target, code := resolveMediaSource(ctx, *r.Source)
		if code != "" {
			drop()
			fail(code)
			return
		}
		if !c.sessionGrant(PermMedia, "play media in an external player",
			"The launcher opens your media player on "+target) {
			drop()
			fail("source blocked")
			return
		}
		player, err := host.MediaPlay(MediaRequest{
			URL:      target,
			MimeType: r.Source.MimeType,
			Title:    mediaTitle(ms.metadata),
			Live:     r.Live,
			Autoplay: r.Autoplay,
		}, func(st MediaState) { c.ci.mediaPlayerState(c.gen, ms.id, st) })
		if err != nil {
			log.Warn().Err(err).Str("napplet", c.ci.napp.ID).Msg("media player did not start")
			drop()
			fail("no media player")
			return
		}

		s.mu.Lock()
		if s.gen != c.gen || s.media[ms.id] != ms {
			// reset or destroyed while the player was starting
			s.mu.Unlock()
			player.Stop()
			return
		}
		ms.player = player
		first := ms.takeState(time.Now(), true)
		s.mu.Unlock()

		c.reply(map[string]any{"sessionId": ms.id, "owner": ms.owner})
		envs := []any{
			map[string]any{"type": "media.capabilities", "sessionId": ms.id, "actions": ms.actions},
			map[string]any{"type": "media.controls", "sessionId": ms.id, "controls": ms.actions},
		}
		if first != nil {
			envs = append(envs, mediaStateEnvelope(ms.id, *first))
		}
		c.ci.napPushGen(c.gen, envs...)
	})
}

// resolveMediaSource turns a source reference into the https url the player
// opens, or names why it can't.
func resolveMediaSource(ctx context.Context, src mediaSourceRef) (string, string) {
	switch {
	case src.URL != "":
		u, err := url.Parse(strings.TrimSpace(src.URL))
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return "", "unsupported source"
		}
		// the player fetches on its own, so this is the one check the
		// launcher gets: a host that names a private address is refused
		if err := publicHost(ctx, u.Hostname()); err != nil {
			return "", "source blocked"
		}
		return u.String(), ""
	case src.BlossomHash != "":
		sha := strings.TrimPrefix(strings.ToLower(src.BlossomHash), "sha256:")
		if !hex64.MatchString(sha) {
			return "", "unsupported source"
		}
		for _, srv := range blossomServers(ctx, nil) {
			if blossomHas(ctx, srv+"/"+sha) {
				return srv + "/" + sha, ""
			}
		}
		return "", "source not found"
	}
	return "", "unsupported source"
}

// blossomHas asks a server whether it has a blob, without downloading it.
func blossomHas(ctx context.Context, target string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "verdana-napplet-resource")
	resp, err := resourceClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func mediaTitle(metadata map[string]any) string {
	title, _ := metadata["title"].(string)
	if artist, _ := metadata["artist"].(string); artist != "" && title != "" {
		title = artist + " – " + title
	}
	return napLinkLabel(title)
}

// mediaActionList keeps the known actions of a napplet-supplied list, once
// each.
func mediaActionList(in []string) []string {
	out := []string{}
	for _, a := range in {
		if slices.Contains(mediaActions, a) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

// mediaLookup finds a session of this call's generation.
func (c *napCall) mediaLookup(id string) *mediaSession {
	s := c.ci.nap
	if s.gen != c.gen {
		return nil
	}
	return s.media[id]
}

func napMediaUpdate(c *napCall) {
	var r struct {
		SessionID string         `json:"sessionId"`
		Metadata  map[string]any `json:"metadata"`
	}
	if c.decode(&r) != nil {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	ms := c.mediaLookup(r.SessionID)
	if ms == nil {
		s.mu.Unlock()
		return
	}
	before := mediaTitle(ms.metadata)
	for k, v := range r.Metadata {
		ms.metadata[k] = v
	}
	title := mediaTitle(ms.metadata)
	player := ms.player
	s.mu.Unlock()
	if player != nil && title != before {
		player.SetTitle(title)
	}
}

func napMediaDestroy(c *napCall) {
	var r struct {
		SessionID string `json:"sessionId"`
	}
	if c.decode(&r) != nil {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	ms := c.mediaLookup(r.SessionID)
	if ms != nil {
		delete(s.media, ms.id)
	}
	s.mu.Unlock()
	if ms != nil {
		ms.stop()
	}
}

// napMediaState is a napplet reporting its own playback. The launcher has
// nowhere to show it yet, but keeps it as the session's state.
func napMediaState(c *napCall) {
	var r struct {
		SessionID string   `json:"sessionId"`
		Status    string   `json:"status"`
		Position  *float64 `json:"position"`
		Duration  *float64 `json:"duration"`
		Volume    *float64 `json:"volume"`
	}
	if c.decode(&r) != nil || !slices.Contains(mediaStatuses, r.Status) {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := c.mediaLookup(r.SessionID)
	if ms == nil || ms.owner != "napplet" {
		return
	}
	ms.state = mediaStateEnvelope(ms.id, MediaState{
		Status: r.Status, Position: r.Position, Duration: r.Duration, Volume: r.Volume,
	})
}

func napMediaCapabilities(c *napCall) {
	var r struct {
		SessionID string   `json:"sessionId"`
		Actions   []string `json:"actions"`
	}
	if c.decode(&r) != nil {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := c.mediaLookup(r.SessionID)
	if ms == nil || ms.owner != "napplet" {
		return
	}
	ms.actions = mediaActionList(r.Actions)
}

// napMediaCommand is the napplet controlling a shell-owned session. Anything
// it may not ask for is dropped: an unknown session, one it owns itself, an
// action the launcher didn't offer, a value out of range.
func napMediaCommand(c *napCall) {
	var r struct {
		SessionID string   `json:"sessionId"`
		Action    string   `json:"action"`
		Value     *float64 `json:"value"`
	}
	if c.decode(&r) != nil {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	ms := c.mediaLookup(r.SessionID)
	if ms == nil || ms.owner != "shell" || ms.player == nil || !slices.Contains(ms.actions, r.Action) {
		s.mu.Unlock()
		return
	}
	player := ms.player
	s.mu.Unlock()

	var err error
	switch r.Action {
	case "play":
		err = player.Play()
	case "pause":
		err = player.Pause()
	case "stop":
		err = player.Stop()
	case "seek":
		if r.Value == nil || *r.Value < 0 {
			return
		}
		err = player.Seek(*r.Value)
	case "volume":
		if r.Value == nil || *r.Value < 0 || *r.Value > 1 {
			return
		}
		err = player.SetVolume(*r.Value)
	}
	if err != nil {
		log.Debug().Err(err).Str("action", r.Action).Str("session", ms.id).Msg("media command failed")
	}
}

// mediaPlayerState is a shell-owned session's player reporting in.
func (ci *Instance) mediaPlayerState(gen int, id string, st MediaState) {
	s := ci.nap
	s.mu.Lock()
	ms := s.media[id]
	if s.gen != gen || ms == nil {
		s.mu.Unlock()
		return
	}
	ms.last = &st
	var push *MediaState
	if ms.player != nil {
		// before the player is attached, create's reply sends the latest
		push = ms.takeState(time.Now(), false)
	}
	s.mu.Unlock()
	if push != nil {
		ci.napPushGen(gen, mediaStateEnvelope(id, *push))
	}
}

// takeState returns the latest player state if the napplet should be sent
// it now, and marks it sent. Anything but the position moving goes at once;
// the position alone, at most every mediaStateInterval. The caller holds
// the session's lock.
func (ms *mediaSession) takeState(now time.Time, force bool) *MediaState {
	st := ms.last
	if st == nil {
		return nil
	}
	if !force && ms.pushed != nil {
		p := ms.pushed
		same := p.Status == st.Status && floatEq(p.Duration, st.Duration) && floatEq(p.Volume, st.Volume)
		if same && (floatEq(p.Position, st.Position) || now.Sub(ms.pushedAt) < mediaStateInterval) {
			return nil
		}
	}
	ms.pushed, ms.pushedAt = st, now
	return st
}

func floatEq(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func mediaStateEnvelope(id string, st MediaState) map[string]any {
	env := map[string]any{"type": "media.state", "sessionId": id, "status": st.Status}
	if st.Position != nil {
		env["position"] = *st.Position
	}
	if st.Duration != nil {
		env["duration"] = *st.Duration
	}
	if st.Volume != nil {
		env["volume"] = *st.Volume
	}
	return env
}
