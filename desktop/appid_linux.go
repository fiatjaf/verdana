//go:build linux

package main

import "gioui.org/app"

// appID is what the launcher window calls itself to the desktop: the wayland
// app_id and the X11 WM_CLASS, which is how a shell matches a running window
// to the com.verdana.app.desktop entry the packages install (see
// packaging/linux) and puts the right icon and name on it. Gio would default
// to the executable's file name, which is not the name of that entry.
const appID = "com.verdana.app"

func init() {
	app.ID = appID
}
