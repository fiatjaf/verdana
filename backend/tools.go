//go:build tools

package backend

// gomobile's bind step (run by whoever builds the Android app) links
// golang.org/x/mobile/bind against this module, so the requirement has to
// survive go mod tidy even though nothing here imports it.
import _ "golang.org/x/mobile/bind"
