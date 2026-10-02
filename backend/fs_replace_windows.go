//go:build windows

package backend

import (
	"os"
	"path/filepath"

	winapi "golang.org/x/sys/windows"
)

func fsCommitReplace(root *os.Root, temporary, destination string) error {
	from, err := winapi.UTF16PtrFromString(filepath.Join(root.Name(), filepath.FromSlash(temporary)))
	if err != nil {
		return err
	}
	to, err := winapi.UTF16PtrFromString(filepath.Join(root.Name(), filepath.FromSlash(destination)))
	if err != nil {
		return err
	}
	return winapi.MoveFileEx(from, to, winapi.MOVEFILE_REPLACE_EXISTING|winapi.MOVEFILE_WRITE_THROUGH)
}
