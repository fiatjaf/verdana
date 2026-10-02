package backend

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// NAP-INC: messages between napplets. Topics are broadcast by exact name to
// every other napplet window listening on them; channels are one-to-one
// pipes between two windows. The sender is always stamped here, from the
// window the envelope came through, never taken from the message.
//
// A topic subscription doubles as a napplet's "ready for this intent"
// signal: subscribing to napplet:<role>/<action> registers that action on
// the window, which is what action dispatch (and so NAP-INTENT) waits for.

func init() {
	handleNap(map[string]napHandler{
		"inc.emit":        napIncEmit,
		"inc.subscribe":   napIncSubscribe,
		"inc.unsubscribe": napIncUnsubscribe,

		"inc.channel.open":      napIncChannelOpen,
		"inc.channel.emit":      napIncChannelEmit,
		"inc.channel.broadcast": napIncChannelBroadcast,
		"inc.channel.list":      napIncChannelList,
		"inc.channel.close":     napIncChannelClose,
	})
}

// incActionIdx marks an action registered by an inc subscription: there is
// no handler index to call, the action is delivered as an inc.event.
const incActionIdx = -2

type incChannel struct {
	id   string
	a, b *Instance
}

// other is the end of the channel that isn't ci, or nil if ci isn't on it.
func (ch *incChannel) other(ci *Instance) *Instance {
	switch ci {
	case ch.a:
		return ch.b
	case ch.b:
		return ch.a
	}
	return nil
}

var (
	incMu       sync.Mutex
	incChannels = map[string]*incChannel{}
)

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// incSender is how a napplet is named to its peers: its d-tag.
func incSender(ci *Instance) string {
	if ci.napp.D == "" {
		// a root napplet (kind 15129) has no d tag: its address names it
		return ci.napp.Address()
	}
	return ci.napp.D
}

// liveNapplets is every napplet window with a running session.
func liveNapplets() []*Instance {
	out := []*Instance{}
	for _, ci := range allInstances() {
		if ci.nap == nil {
			continue
		}
		ci.nap.mu.Lock()
		ok := ci.nap.established
		ci.nap.mu.Unlock()
		if ok {
			out = append(out, ci)
		}
	}
	return out
}

// ─── topics ──────────────────────────────────────────────────────

func napIncEmit(c *napCall) {
	var r struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := c.decode(&r); err != nil || r.Topic == "" {
		return
	}
	incPublish(c.ci, r.Topic, r.Payload)
}

// incPublish delivers a topic event to every other napplet listening on it.
func incPublish(from *Instance, topic string, payload json.RawMessage) {
	ev := map[string]any{"type": "inc.event", "topic": topic, "sender": incSender(from)}
	if len(payload) > 0 {
		ev["payload"] = payload
	}
	for _, ci := range liveNapplets() {
		if ci == from {
			continue
		}
		ci.nap.mu.Lock()
		listening := ci.nap.topics[topic]
		ci.nap.mu.Unlock()
		if listening {
			ci.napPush(ev)
		}
	}
}

func napIncSubscribe(c *napCall) {
	var r struct {
		Topic string `json:"topic"`
	}
	if err := c.decode(&r); err != nil || r.Topic == "" {
		c.reply(map[string]any{"error": "invalid topic"})
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	s.topics[r.Topic] = true
	s.mu.Unlock()
	c.ci.registerAction(r.Topic, incActionIdx)
	c.reply(nil)
}

func napIncUnsubscribe(c *napCall) {
	var r struct {
		Topic string `json:"topic"`
	}
	if err := c.decode(&r); err != nil || r.Topic == "" {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	delete(s.topics, r.Topic)
	s.mu.Unlock()
	c.ci.unregisterAction(r.Topic)
}

// ─── channels ────────────────────────────────────────────────────

func napIncChannelOpen(c *napCall) {
	var r struct {
		Target string `json:"target"`
	}
	if err := c.decode(&r); err != nil || r.Target == "" {
		c.reply(map[string]any{"error": "invalid target"})
		return
	}
	var peer *Instance
	for _, ci := range liveNapplets() {
		if ci != c.ci && ci.napp.D == r.Target {
			peer = ci
			break
		}
	}
	if peer == nil {
		c.reply(map[string]any{"error": "target not available"})
		return
	}

	ch := &incChannel{id: randomID(), a: c.ci, b: peer}
	incMu.Lock()
	incChannels[ch.id] = ch
	incMu.Unlock()

	// the target hears about it before the opener can use it
	peer.napPush(map[string]any{"type": "inc.channel.opened", "channelId": ch.id, "peer": incSender(c.ci)})
	c.reply(map[string]any{"channelId": ch.id, "peer": incSender(peer)})
}

func napIncChannelEmit(c *napCall) {
	var r struct {
		ChannelID string          `json:"channelId"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := c.decode(&r); err != nil {
		return
	}
	incMu.Lock()
	ch := incChannels[r.ChannelID]
	incMu.Unlock()
	if ch == nil {
		return
	}
	if peer := ch.other(c.ci); peer != nil {
		peer.napPush(incChannelEvent(ch.id, c.ci, r.Payload))
	}
}

func incChannelEvent(id string, from *Instance, payload json.RawMessage) map[string]any {
	ev := map[string]any{"type": "inc.channel.event", "channelId": id, "sender": incSender(from)}
	if len(payload) > 0 {
		ev["payload"] = payload
	}
	return ev
}

func napIncChannelBroadcast(c *napCall) {
	var r struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := c.decode(&r); err != nil {
		return
	}
	for _, ch := range incChannelsOf(c.ci) {
		if peer := ch.other(c.ci); peer != nil {
			peer.napPush(incChannelEvent(ch.id, c.ci, r.Payload))
		}
	}
}

func incChannelsOf(ci *Instance) []*incChannel {
	incMu.Lock()
	defer incMu.Unlock()
	out := []*incChannel{}
	for _, ch := range incChannels {
		if ch.other(ci) != nil {
			out = append(out, ch)
		}
	}
	return out
}

func napIncChannelList(c *napCall) {
	list := []map[string]any{}
	for _, ch := range incChannelsOf(c.ci) {
		list = append(list, map[string]any{"id": ch.id, "peer": incSender(ch.other(c.ci))})
	}
	c.reply(map[string]any{"channels": list})
}

func napIncChannelClose(c *napCall) {
	var r struct {
		ChannelID string `json:"channelId"`
	}
	if err := c.decode(&r); err != nil {
		return
	}
	incMu.Lock()
	ch := incChannels[r.ChannelID]
	if ch == nil || ch.other(c.ci) == nil {
		incMu.Unlock()
		return
	}
	delete(incChannels, ch.id)
	incMu.Unlock()
	closed := map[string]any{"type": "inc.channel.closed", "channelId": ch.id, "reason": "closed by peer"}
	ch.a.napPush(closed)
	ch.b.napPush(closed)
}

// incForget drops everything a window's session had in INC: its channels
// (their peers are told why) and its topic registrations. Called with the
// window's session lock held, so peers are notified from a goroutine.
func incForget(ci *Instance, reason string) {
	incMu.Lock()
	var gone []*incChannel
	for id, ch := range incChannels {
		if ch.other(ci) != nil {
			gone = append(gone, ch)
			delete(incChannels, id)
		}
	}
	incMu.Unlock()
	for _, ch := range gone {
		peer, id := ch.other(ci), ch.id
		go peer.napPush(map[string]any{"type": "inc.channel.closed", "channelId": id, "reason": reason})
	}
	for topic := range ci.nap.topics {
		ci.unregisterAction(topic)
	}
}
