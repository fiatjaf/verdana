//go:build linux

package main

import (
	"image/color"
	"math"
	"sync"

	"github.com/godbus/dbus/v5"
)

const (
	portalBus       = "org.freedesktop.portal.Desktop"
	portalPath      = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalSettings  = "org.freedesktop.portal.Settings"
	appearanceSpace = "org.freedesktop.appearance"
)

func watchSystemAppearance() (systemAppearance, <-chan systemAppearance, func()) {
	changes := make(chan systemAppearance, 1)
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		close(changes)
		return systemAppearance{}, changes, func() {}
	}
	obj := conn.Object(portalBus, portalPath)
	current := readPortalAppearance(obj)

	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	err = conn.AddMatchSignal(
		dbus.WithMatchObjectPath(portalPath),
		dbus.WithMatchInterface(portalSettings),
		dbus.WithMatchMember("SettingChanged"),
	)
	if err != nil {
		conn.RemoveSignal(signals)
		conn.Close()
		close(changes)
		return current, changes, func() {}
	}
	stopped := make(chan struct{})
	go func() {
		defer close(changes)
		for {
			select {
			case <-stopped:
				return
			case signal := <-signals:
				if signal == nil || len(signal.Body) < 3 {
					continue
				}
				namespace, _ := signal.Body[0].(string)
				key, _ := signal.Body[1].(string)
				if namespace != appearanceSpace || (key != "color-scheme" && key != "accent-color") {
					continue
				}
				current = readPortalAppearance(obj)
				select {
				case changes <- current:
				default:
					<-changes
					changes <- current
				}
			}
		}
	}()
	var stopOnce sync.Once
	return current, changes, func() {
		stopOnce.Do(func() {
			close(stopped)
			conn.RemoveSignal(signals)
			conn.Close()
		})
	}
}

func readPortalAppearance(obj dbus.BusObject) systemAppearance {
	var out systemAppearance
	if value, ok := readPortalSetting(obj, "color-scheme"); ok {
		if scheme, ok := uintValue(value); ok {
			out.dark = scheme == 1
		}
	}
	if value, ok := readPortalSetting(obj, "accent-color"); ok {
		if accent, ok := accentValue(value); ok {
			out.accent, out.hasAccent = accent, true
		}
	}
	return out
}

func readPortalSetting(obj dbus.BusObject, key string) (any, bool) {
	var value dbus.Variant
	err := obj.Call(portalSettings+".ReadOne", 0, appearanceSpace, key).Store(&value)
	if err != nil {
		err = obj.Call(portalSettings+".Read", 0, appearanceSpace, key).Store(&value)
	}
	if err != nil {
		return nil, false
	}
	return unwrapVariant(value), true
}

func unwrapVariant(value any) any {
	for {
		variant, ok := value.(dbus.Variant)
		if !ok {
			return value
		}
		value = variant.Value()
	}
}

func uintValue(value any) (uint32, bool) {
	value = unwrapVariant(value)
	switch value := value.(type) {
	case uint32:
		return value, true
	case uint64:
		return uint32(value), true
	default:
		return 0, false
	}
}

func accentValue(value any) (color.NRGBA, bool) {
	value = unwrapVariant(value)
	components := [3]float64{}
	switch items := value.(type) {
	case []interface{}:
		if len(items) != 3 {
			return color.NRGBA{}, false
		}
		for i, item := range items {
			component, ok := item.(float64)
			if !ok {
				return color.NRGBA{}, false
			}
			components[i] = component
		}
	case []float64:
		if len(items) != 3 {
			return color.NRGBA{}, false
		}
		copy(components[:], items)
	case [3]float64:
		components = items
	default:
		return color.NRGBA{}, false
	}
	rgb := [3]uint8{}
	for i, component := range components {
		if component < 0 || component > 1 {
			return color.NRGBA{}, false
		}
		rgb[i] = uint8(math.Round(component * 255))
	}
	return color.NRGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}, true
}
