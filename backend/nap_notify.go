package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxNotificationTitle = 200
	maxNotificationBody  = 2000
	maxNotificationLabel = 80
)

type notificationAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type notificationChannel struct {
	ChannelID       string `json:"channelId"`
	Label           string `json:"label"`
	Description     string `json:"description"`
	DefaultPriority string `json:"defaultPriority"`
}

type notificationSendRequest struct {
	Title    string               `json:"title"`
	Body     string               `json:"body"`
	Icon     string               `json:"icon"`
	Actions  []notificationAction `json:"actions"`
	Channel  string               `json:"channel"`
	Priority string               `json:"priority"`
}

func init() {
	handleNap(map[string]napHandler{
		"notify.send":               napNotifySend,
		"notify.dismiss":            napNotifyDismiss,
		"notify.badge":              napNotifyBadge,
		"notify.channel.register":   napNotifyRegisterChannel,
		"notify.permission.request": napNotifyPermission,
	})
}

// napNotifyPermission answers as notify.permission.result, the type the shim
// settles notify.permission.request on (not the usual <type>.result).
func napNotifyPermission(c *napCall) {
	var req struct {
		Channel string `json:"channel"`
	}
	if c.decode(&req) != nil {
		c.replyAs("notify.permission.result", map[string]any{"granted": false})
		return
	}
	c.async(func(context.Context) {
		detail := "The napplet can show plain-text notifications using your system notification service."
		if req.Channel != "" {
			detail += " It requested the " + cleanNotificationText(req.Channel, 100) + " channel."
		}
		granted := c.sessionGrant(PermNotify, "show system notifications", detail)
		if granted {
			granted = host.RequestNotificationPermission()
		}
		c.replyAs("notify.permission.result", map[string]any{"granted": granted})
	})
}

func napNotifySend(c *napCall) {
	var req notificationSendRequest
	if err := c.decode(&req); err != nil {
		c.reply(map[string]any{"error": "invalid notification"})
		return
	}
	if err := validateNotification(&req); err != nil {
		c.reply(map[string]any{"error": err.Error()})
		return
	}
	if !c.notificationGranted() {
		c.reply(map[string]any{"error": "permission denied"})
		return
	}

	s := c.ci.nap
	s.mu.Lock()
	if req.Channel != "" {
		if _, ok := s.notifyChannels[req.Channel]; !ok {
			s.mu.Unlock()
			c.reply(map[string]any{"error": "invalid channel"})
			return
		}
	}
	now := time.Now()
	s.notifyTimes = recentNotifications(s.notifyTimes, now)
	s.urgentNotifyTimes = recentNotifications(s.urgentNotifyTimes, now)
	if len(s.notifyTimes) >= 20 || (req.Priority == "urgent" && len(s.urgentNotifyTimes) >= 3) {
		s.mu.Unlock()
		c.reply(map[string]any{"error": "rate limited"})
		return
	}
	s.notifyTimes = append(s.notifyTimes, now)
	if req.Priority == "urgent" {
		s.urgentNotifyTimes = append(s.urgentNotifyTimes, now)
	}
	s.notifySeq++
	id := fmt.Sprintf("%s-%d", c.ci.instance, s.notifySeq)
	s.mu.Unlock()

	actions := make([]NotificationAction, len(req.Actions))
	for i, action := range req.Actions {
		actions[i] = NotificationAction{ID: action.ID, Label: action.Label}
	}
	c.async(func(context.Context) {
		handle, err := host.SendNotification(NotificationRequest{
			ID: id, NappID: c.ci.napp.ID, NappName: c.ci.napp.Label(),
			Title: req.Title, Body: req.Body, Icon: req.Icon, Channel: req.Channel,
			Priority: req.Priority, Actions: actions,
		})
		if err != nil || handle == nil {
			if err == nil {
				err = errors.New("notification service returned no handle")
			}
			log.Warn().Err(err).Str("napplet", c.ci.napp.ID).Msg("system notification failed")
			c.reply(map[string]any{"error": "notification unavailable"})
			return
		}
		s.mu.Lock()
		if s.gen != c.gen {
			s.mu.Unlock()
			if handle != nil {
				_ = handle.Dismiss()
			}
			return
		}
		s.notifications[id] = handle
		s.mu.Unlock()
		c.reply(map[string]any{"notificationId": id})
	})
}

func recentNotifications(times []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-time.Minute)
	first := 0
	for first < len(times) && times[first].Before(cutoff) {
		first++
	}
	return times[first:]
}

func napNotifyDismiss(c *napCall) {
	var req struct {
		NotificationID string `json:"notificationId"`
	}
	if c.decode(&req) != nil || req.NotificationID == "" {
		return
	}
	s := c.ci.nap
	s.mu.Lock()
	handle, ok := s.notifications[req.NotificationID]
	if ok {
		delete(s.notifications, req.NotificationID)
	}
	s.mu.Unlock()
	if ok && handle != nil {
		_ = handle.Dismiss()
	}
}

func napNotifyBadge(c *napCall) {
	var req struct {
		Count int64 `json:"count"`
	}
	if c.decode(&req) != nil || req.Count < 0 {
		return
	}
	c.ci.nap.mu.Lock()
	c.ci.nap.notifyBadge = uint(req.Count)
	c.ci.nap.mu.Unlock()
}

func napNotifyRegisterChannel(c *napCall) {
	var channel notificationChannel
	if c.decode(&channel) != nil || channel.ChannelID == "" || channel.Label == "" ||
		!shortPlain(channel.ChannelID, 100) || !shortPlain(channel.Label, maxNotificationLabel) ||
		(channel.DefaultPriority != "" && !validNotificationPriority(channel.DefaultPriority)) {
		return
	}
	channel.Description = cleanNotificationText(channel.Description, 500)
	c.ci.nap.mu.Lock()
	c.ci.nap.notifyChannels[channel.ChannelID] = channel
	c.ci.nap.mu.Unlock()
}

func (c *napCall) notificationGranted() bool {
	s := c.ci.nap
	s.mu.Lock()
	granted, ok := s.grants[PermNotify]
	s.mu.Unlock()
	if ok {
		return granted
	}
	rule, ok := lookupRule(RuleKey{Napp: c.ci.napp.ID, Permission: PermNotify})
	return ok && rule.Decision.granted()
}

func validateNotification(req *notificationSendRequest) error {
	req.Title = cleanNotificationText(req.Title, maxNotificationTitle)
	req.Body = cleanNotificationText(req.Body, maxNotificationBody)
	if req.Title == "" {
		return errors.New("invalid notification")
	}
	if req.Icon != "" {
		return errors.New("unsupported icon")
	}
	if req.Priority == "" {
		req.Priority = "normal"
	}
	if !validNotificationPriority(req.Priority) || len(req.Actions) > 3 {
		return errors.New("invalid notification")
	}
	seen := make(map[string]bool, len(req.Actions))
	for i := range req.Actions {
		req.Actions[i].ID = strings.TrimSpace(req.Actions[i].ID)
		req.Actions[i].Label = cleanNotificationText(req.Actions[i].Label, maxNotificationLabel)
		if req.Actions[i].ID == "" || req.Actions[i].Label == "" ||
			!shortPlain(req.Actions[i].ID, 100) || seen[req.Actions[i].ID] {
			return errors.New("invalid notification")
		}
		seen[req.Actions[i].ID] = true
	}
	return nil
}

func validNotificationPriority(priority string) bool {
	switch priority {
	case "low", "normal", "high", "urgent":
		return true
	}
	return false
}

func shortPlain(s string, max int) bool {
	return s == cleanNotificationText(s, max) && s != ""
}

func cleanNotificationText(s string, max int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !utf8.ValidRune(r) || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:max]))
}
