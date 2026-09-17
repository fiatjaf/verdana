//go:build !dev

package main

import (
	"errors"

	"gioui.org/widget"
)

// Production builds have no dev tab: the flag hides it and the picker is a
// stub (unreachable, but the shared layout code still references it).

const devEnabled = false

func pickAndLoadFolder(pathEd *widget.Editor) {}

func pickFolder() (string, error) { return "", errors.New("folder picker needs a dev build") }
