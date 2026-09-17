//go:build dev

package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"

	"gioui.org/widget"
	"verdana/backend"
)

// This file is only compiled with the dev tag (see justfile run): it holds
// the dev tab's enable flag and the native folder picker behind its Browse
// button.

const devEnabled = true

// pickAndLoadFolder opens the platform's folder picker and loads whatever
// folder the user picked as a dev napp.
func pickAndLoadFolder(pathEd *widget.Editor) {
	path, err := pickFolder()
	if err != nil {
		backend.SetDevErr(err.Error())
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	pathEd.SetText(path)
	if gioWin != nil {
		gioWin.Invalidate()
	}
	go backend.DevLoadFolder(path)
}

// pickFolder opens a native folder picker and returns the chosen folder. A
// cancelled dialog is an error too, so the caller just shows it and moves on.
func pickFolder() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("osascript",
			"-e", `POSIX path of (choose folder with prompt "Pick a napp folder")`).Output()
		if err != nil {
			return "", errors.New("folder picker cancelled")
		}
		return strings.TrimSpace(string(out)), nil
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms; ` +
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
			`$d.Description = 'Pick a napp folder'; ` +
			`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`
		out, err := exec.Command("powershell", "-NoProfile", "-Command", script).Output()
		if err != nil {
			return "", errors.New("folder picker cancelled")
		}
		if p := strings.TrimSpace(string(out)); p != "" {
			return p, nil
		}
		return "", errors.New("folder picker cancelled")
	default:
		// zenity and kdialog are the native dialogs on linux desktops
		if _, err := exec.LookPath("zenity"); err == nil {
			out, err := exec.Command("zenity",
				"--file-selection", "--directory", "--title=Pick a napp folder").Output()
			if err != nil {
				return "", errors.New("folder picker cancelled")
			}
			return strings.TrimSpace(string(out)), nil
		}
		if _, err := exec.LookPath("kdialog"); err == nil {
			out, err := exec.Command("kdialog",
				"--title", "Pick a napp folder", "--getexistingdirectory").Output()
			if err != nil {
				return "", errors.New("folder picker cancelled")
			}
			return strings.TrimSpace(string(out)), nil
		}
		return "", errors.New("no folder picker found (install zenity), paste the folder path instead")
	}
}
