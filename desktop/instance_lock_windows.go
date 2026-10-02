//go:build windows

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"golang.org/x/sys/windows"
)

func acquireInstanceLock(dataDir string) (release func(), acquired bool, err error) {
	sum := sha256.Sum256([]byte(dataDir))
	name, err := windows.UTF16PtrFromString(`Local\Verdana-` + hex.EncodeToString(sum[:8]))
	if err != nil {
		return nil, false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		windows.CloseHandle(handle)
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() { windows.CloseHandle(handle) }, true, nil
}
