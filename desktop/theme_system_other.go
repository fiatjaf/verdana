//go:build !linux

package main

// Native observers for Windows and macOS are added independently. Until
// then system mode safely follows the light fallback on those platforms.
func watchSystemAppearance() (systemAppearance, <-chan systemAppearance, func()) {
	changes := make(chan systemAppearance)
	close(changes)
	return systemAppearance{}, changes, func() {}
}
