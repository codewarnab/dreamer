package web

import (
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
