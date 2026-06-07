package pathjointest

import (
	"net/url"
	"path"
	"path/filepath"
)

// good uses path.* only for URLs/slash paths — should produce zero diagnostics.
func good(u *url.URL, endpoint string) string {
	// URL evidence: arg derives from net/url.
	joined := path.Join(u.Path, "sub")

	// URL-ish name suppresses, even though "urlPath" also matches the fs pattern.
	urlPath := "/api/v1"
	joined += path.Join(urlPath, endpoint)

	// No filesystem evidence at all: bare literals are fine.
	joined += path.Join("a", "b")

	// filepath.Join on a path-named identifier is the correct call — not flagged.
	configPath := "x"
	return joined + filepath.Join(configPath, "y")
}
