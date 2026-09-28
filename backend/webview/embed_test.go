package webview

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestUIKitScriptOnlyForNappsThatAskForIt(t *testing.T) {
	for _, requires := range [][]string{nil, {}, {"nostr"}, {"ui", "nostr"}} {
		script := UIKitScript(requires)
		hasUI := false
		for _, r := range requires {
			if r == "ui" {
				hasUI = true
			}
		}
		if hasUI != (script != "") {
			t.Fatalf("UIKitScript(%q) = %d bytes, wanted a script: %v", requires, len(script), hasUI)
		}
	}
}

func TestUIKitScriptCarriesTheKit(t *testing.T) {
	script := UIKitScript([]string{"ui"})

	// the whole stylesheet travels as one JSON string, so quotes, newlines
	// and the data: uris of the glyphs cannot break out of it
	marker := "var css = "
	start := strings.Index(script, marker)
	if start < 0 {
		t.Fatal("no stylesheet in the kit script")
	}
	var css string
	if err := json.NewDecoder(strings.NewReader(script[start+len(marker):])).Decode(&css); err != nil {
		t.Fatalf("stylesheet is not a JSON string: %v", err)
	}
	if css != uiCSS {
		t.Fatal("the script does not carry napp-ui.css whole")
	}
	if !strings.Contains(css, ".v-btn--accent") {
		t.Fatal("no buttons in the kit")
	}

	// it installs one <style>, and waits for a document to install it in
	if !strings.Contains(script, "createElement('style')") {
		t.Fatal("the kit is not a <style>")
	}
	if !strings.Contains(script, "MutationObserver") {
		t.Fatal("the kit does not wait for a document")
	}
}

// glyphRe finds the rule for one of the kit's glyph classes.
var glyphRe = regexp.MustCompile(`(?s)\.v-icon--([a-z]+) \{\s*(-webkit-mask-image|mask-image): url\("([^"]+)"\);`)

// TestEveryGlyphIsDrawable guards the glyph classes: a hand-written base64
// svg is easy to mistype, and a broken one draws nothing at all — the icon
// is simply missing, with no error anywhere. So every one of them has to
// decode, be an svg, and be the same drawing in both properties.
func TestEveryGlyphIsDrawable(t *testing.T) {
	seen := map[string]string{}
	for _, m := range glyphRe.FindAllStringSubmatch(uiCSS, -1) {
		name, property, uri := m[1], m[2], m[3]

		payload, ok := strings.CutPrefix(uri, "data:image/svg+xml;base64,")
		if !ok {
			t.Errorf(".v-icon--%s: %s is not a base64 svg", name, property)
			continue
		}
		svg, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			t.Errorf(".v-icon--%s: %s does not decode: %v", name, property, err)
			continue
		}
		if !strings.HasPrefix(string(svg), "<svg") || !strings.Contains(string(svg), "<path") &&
			!strings.Contains(string(svg), "<circle") {
			t.Errorf(".v-icon--%s: %s is not a drawing", name, property)
			continue
		}
		if before, ok := seen[name]; ok && before != string(svg) {
			t.Errorf(".v-icon--%s: %s draws something else than the other property", name, property)
		}
		seen[name] = string(svg)
	}

	for _, want := range []string{"check", "x", "warning", "info", "trash", "search", "sun", "window"} {
		if _, ok := seen[want]; !ok {
			t.Errorf("no .v-icon--%s in the kit", want)
		}
	}
	if len(seen) < 20 {
		t.Errorf("only %d glyphs in the kit, expected the whole set", len(seen))
	}
}
