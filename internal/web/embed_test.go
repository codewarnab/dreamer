package web

import (
	"bytes"
	"io/fs"
	"testing"
)

func TestEmbed_FindsTemplatesAndStatic(t *testing.T) {
	checks := []string{
		"templates/layout.html",
		"static/vendor/htmx.min.js",
		"static/vendor/alpine.min.js",
		"static/fonts/Anton/Anton-Regular.woff2",
		"static/fonts/SpaceGrotesk/SpaceGrotesk-Regular.woff2",
		"static/fonts/JetBrainsMono/JetBrainsMono-Regular.woff2",
	}
	for _, p := range checks {
		data, err := fs.ReadFile(assets, p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(data) == 0 {
			t.Fatalf("%s: empty", p)
		}
	}
}

func TestEmbed_DreamerCSSHasVergeTokens(t *testing.T) {
	data, err := fs.ReadFile(assets, "static/css/dreamer.css")
	if err != nil {
		t.Fatalf("read dreamer.css: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dreamer.css: empty")
	}
	if !bytes.Contains(data, []byte("--canvas-black")) {
		t.Fatal("dreamer.css: missing --canvas-black token")
	}
}
