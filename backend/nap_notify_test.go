package backend

import (
	"sync"
	"testing"
	"time"
)

type notifyTestHost struct {
	noopHost
	mu         sync.Mutex
	requests   []NotificationRequest
	handles    []*notifyTestHandle
	err        error
	permission bool
}

func (h *notifyTestHost) RequestNotificationPermission() bool { return h.permission }

func (h *notifyTestHost) NotificationControls() []string {
	return []string{"system"}
}

func (h *notifyTestHost) SendNotification(req NotificationRequest) (NotificationHandle, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return nil, h.err
	}
	handle := &notifyTestHandle{}
	h.requests = append(h.requests, req)
	h.handles = append(h.handles, handle)
	return handle, nil
}

type notifyTestHandle struct {
	mu        sync.Mutex
	dismissed int
}

func (h *notifyTestHandle) Dismiss() error {
	h.mu.Lock()
	h.dismissed++
	h.mu.Unlock()
	return nil
}

func (h *notifyTestHandle) dismissCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dismissed
}

func TestNotifySendDismissAndSessionCleanup(t *testing.T) {
	setupNapTest(t)
	nh := &notifyTestHost{}
	host = nh
	ci, rec := openNapplet(t, "notify")
	ready(t, ci, rec, 1)

	controls := rec.wait(t, "notify.controls", 1)
	gotControls, _ := controls["controls"].([]any)
	if len(gotControls) != 1 || gotControls[0] != "system" {
		t.Fatalf("controls = %#v", controls["controls"])
	}

	ci.nap.mu.Lock()
	ci.nap.grants[PermNotify] = true
	ci.nap.mu.Unlock()
	post(t, ci, map[string]any{
		"type": "notify.send", "id": "send-1", "title": "Hello",
		"body": "from a napplet", "priority": "high",
	})
	result := rec.wait(t, "notify.send.result", 1)
	notificationID, _ := result["notificationId"].(string)
	if notificationID == "" {
		t.Fatalf("send result = %#v", result)
	}
	if len(nh.requests) != 1 || nh.requests[0].Title != "Hello" || nh.requests[0].Priority != "high" {
		t.Fatalf("requests = %#v", nh.requests)
	}

	post(t, ci, map[string]any{"type": "notify.dismiss", "notificationId": notificationID})
	deadline := time.Now().Add(time.Second)
	for nh.handles[0].dismissCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := nh.handles[0].dismissCount(); got != 1 {
		t.Fatalf("dismiss count = %d", got)
	}

	post(t, ci, map[string]any{"type": "notify.send", "id": "send-2", "title": "Again"})
	rec.wait(t, "notify.send.result", 2)
	ci.napReset()
	if got := nh.handles[1].dismissCount(); got != 1 {
		t.Fatalf("reset dismiss count = %d", got)
	}
}

func TestNotifyRejectsUnapprovedAndInvalidRequests(t *testing.T) {
	setupNapTest(t)
	nh := &notifyTestHost{}
	host = nh
	ci, rec := openNapplet(t, "notify-invalid")
	ready(t, ci, rec, 1)

	post(t, ci, map[string]any{"type": "notify.send", "id": "denied", "title": "No grant"})
	if got := rec.wait(t, "notify.send.result", 1)["error"]; got != "permission denied" {
		t.Fatalf("unapproved error = %#v", got)
	}

	ci.nap.mu.Lock()
	ci.nap.grants[PermNotify] = true
	ci.nap.mu.Unlock()
	post(t, ci, map[string]any{"type": "notify.send", "id": "empty", "title": ""})
	if got := rec.wait(t, "notify.send.result", 2)["error"]; got != "invalid notification" {
		t.Fatalf("invalid error = %#v", got)
	}
	post(t, ci, map[string]any{
		"type": "notify.send", "id": "actions", "title": "Too many",
		"actions": []any{
			map[string]any{"id": "1", "label": "One"},
			map[string]any{"id": "2", "label": "Two"},
			map[string]any{"id": "3", "label": "Three"},
			map[string]any{"id": "4", "label": "Four"},
		},
	})
	if got := rec.wait(t, "notify.send.result", 3)["error"]; got != "invalid notification" {
		t.Fatalf("actions error = %#v", got)
	}
	if len(nh.requests) != 0 {
		t.Fatalf("invalid requests reached host: %#v", nh.requests)
	}
}

func TestNotifyChannelMustBeRegistered(t *testing.T) {
	setupNapTest(t)
	nh := &notifyTestHost{}
	host = nh
	ci, rec := openNapplet(t, "notify-channel")
	ready(t, ci, rec, 1)
	ci.nap.mu.Lock()
	ci.nap.grants[PermNotify] = true
	ci.nap.mu.Unlock()

	post(t, ci, map[string]any{
		"type": "notify.send", "id": "missing", "title": "Hello", "channel": "messages",
	})
	if got := rec.wait(t, "notify.send.result", 1)["error"]; got != "invalid channel" {
		t.Fatalf("missing channel error = %#v", got)
	}
	post(t, ci, map[string]any{
		"type": "notify.channel.register", "channelId": "messages", "label": "Messages",
		"defaultPriority": "normal",
	})
	post(t, ci, map[string]any{
		"type": "notify.send", "id": "registered", "title": "Hello", "channel": "messages",
	})
	if got := rec.wait(t, "notify.send.result", 2)["notificationId"]; got == nil {
		t.Fatalf("registered channel result = %#v", rec.find("notify.send.result"))
	}
}

func TestNotifyPermissionCombinesVerdanaAndPlatformApproval(t *testing.T) {
	setupNapTest(t)
	nh := &notifyTestHost{permission: true}
	host = nh
	ci, rec := openNapplet(t, "notify-permission")
	ready(t, ci, rec, 1)
	key := RuleKey{Napp: ci.napp.ID, Permission: PermNotify}
	setSessionRule(key, Rule{Decision: DecisionAllow})
	t.Cleanup(func() { clearSessionRule(key) })

	post(t, ci, map[string]any{"type": "notify.permission.request", "id": "permission"})
	if got := rec.wait(t, "notify.permission.request.result", 1)["granted"]; got != true {
		t.Fatalf("permission result = %#v", got)
	}

	nh.permission = false
	post(t, ci, map[string]any{"type": "notify.permission.request", "id": "os-denied"})
	if got := rec.wait(t, "notify.permission.request.result", 2)["granted"]; got != false {
		t.Fatalf("OS-denied permission result = %#v", got)
	}
}

func TestNotifyRateLimitsEachSession(t *testing.T) {
	setupNapTest(t)
	nh := &notifyTestHost{}
	host = nh
	ci, rec := openNapplet(t, "notify-rate")
	ready(t, ci, rec, 1)
	ci.nap.mu.Lock()
	ci.nap.grants[PermNotify] = true
	ci.nap.mu.Unlock()

	for i := 0; i < 3; i++ {
		post(t, ci, map[string]any{
			"type": "notify.send", "id": i, "title": "Urgent", "priority": "urgent",
		})
		rec.wait(t, "notify.send.result", i+1)
	}
	post(t, ci, map[string]any{
		"type": "notify.send", "id": "limited", "title": "Urgent", "priority": "urgent",
	})
	if got := rec.wait(t, "notify.send.result", 4)["error"]; got != "rate limited" {
		t.Fatalf("rate limit error = %#v", got)
	}
}
