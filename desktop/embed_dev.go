//go:build dev

package main

// A dev build runs the shells out of the source tree, so there is
// nothing to unpack.
func extractShell(string) (string, error) {
	return "", nil
}
