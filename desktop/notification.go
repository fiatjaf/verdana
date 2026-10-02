package main

import (
	"verdana/backend"

	"github.com/gen2brain/beeep"
)

// The desktop tracer deliberately advertises only system delivery. beeep uses
// each desktop's native notification service, but does not expose portable
// action, channel, badge, or dismissal callbacks.
func (gioHost) NotificationControls() []string { return []string{"system"} }
func (gioHost) RequestNotificationPermission() bool { return true }

func (gioHost) SendNotification(req backend.NotificationRequest) (backend.NotificationHandle, error) {
	title := req.NappName + ": " + req.Title
	if err := beeep.Notify(title, req.Body, ""); err != nil {
		return nil, err
	}
	return desktopNotification{}, nil
}

type desktopNotification struct{}

func (desktopNotification) Dismiss() error { return nil }
