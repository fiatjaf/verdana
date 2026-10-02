//go:build !windows

package backend

import "os"

func fsCommitReplace(root *os.Root, temporary, destination string) error {
	return root.Rename(temporary, destination)
}
