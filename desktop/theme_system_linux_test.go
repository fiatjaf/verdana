//go:build linux

package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestPortalAppearanceValues(t *testing.T) {
	if scheme, ok := uintValue(dbus.MakeVariant(dbus.MakeVariant(uint32(1)))); !ok || scheme != 1 {
		t.Fatalf("nested color scheme = %d, %v", scheme, ok)
	}
	accent, ok := accentValue(dbus.MakeVariant([]interface{}{0.25, 0.5, 1.0}))
	if !ok {
		t.Fatal("valid portal accent rejected")
	}
	if accent.R != 64 || accent.G != 128 || accent.B != 255 || accent.A != 255 {
		t.Fatalf("accent = %#v", accent)
	}
}

func TestPortalAccentRejectsInvalidComponents(t *testing.T) {
	if _, ok := accentValue([]interface{}{0.0, 1.1, 0.0}); ok {
		t.Fatal("out-of-range accent accepted")
	}
}

func TestDesktopPortalAppearanceWhenAvailable(t *testing.T) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Skip("no session bus:", err)
	}
	defer conn.Close()
	obj := conn.Object(portalBus, portalPath)
	scheme, ok := readPortalSetting(obj, "color-scheme")
	if !ok {
		t.Skip("appearance portal is unavailable")
	}
	if _, ok := uintValue(scheme); !ok {
		t.Fatalf("portal returned an unsupported color-scheme value: %#v", scheme)
	}
	if accent, ok := readPortalSetting(obj, "accent-color"); ok {
		if _, ok := accentValue(accent); !ok {
			t.Fatalf("portal returned an unsupported accent-color value: %#v", accent)
		}
	}
}
