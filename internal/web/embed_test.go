package web

import (
	"bytes"
	"io/fs"
	"testing"
)

func TestEmbed_FindsTemplatesAndStatic(t *testing.T) {
	checks := []string{
		"templates/layouts/layout.html",
		"templates/pages/dashboard.html",
		"templates/pages/projects/overview.html",
		"templates/pages/projects/findings.html",
		"templates/pages/projects/chats.html",
		"templates/pages/projects/history.html",
		"templates/pages/settings.html",
		"templates/pages/logs.html",
		"templates/pages/providers.html",
		"static/favicon.svg",
		"static/js/app.js",
		"static/js/dashboard.js",
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

// TestParsePageTemplate_AllPages verifies that every page registered in
// initTemplates parses without error from the embedded FS. A syntax error in
// any layout, partial, or page template will fail this test.
func TestParsePageTemplate_AllPages(t *testing.T) {
	pages := []string{
		"dashboard",
		"jobs",
		"job_detail",
		"settings",
		"logs",
		"providers",
		"projects/overview",
		"projects/findings",
		"projects/chats",
		"projects/history",
	}
	for _, page := range pages {
		page := page
		t.Run(page, func(t *testing.T) {
			_, err := parsePageTemplate(assets, page)
			if err != nil {
				t.Errorf("parsePageTemplate(%q) failed: %v", page, err)
			}
		})
	}
}
