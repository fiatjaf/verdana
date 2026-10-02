//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

const windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	_, _, err = key.GetStringValue("Verdana")
	return err == nil
}

func setAutostart(enabled bool, exe string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if !enabled {
		if err := key.DeleteValue("Verdana"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return key.SetStringValue("Verdana", fmt.Sprintf("%q --background", exe))
}
