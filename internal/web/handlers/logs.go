package handlers

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/config"
)

// LogsTail returns an http.HandlerFunc for GET /api/logs/tail.
// Reads the last cfg.Web.LogTailKB*1024 bytes of dreamer.log, optionally
// filters by a leading-token "level" query param. HX-Request wraps in <pre>.
func LogsTail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		kb := cfg.Web.LogTailKB
		if kb <= 0 {
			kb = config.DefaultLogTailKB
		}
		maxBytes := int64(kb) * 1024
		path := filepath.Join(cfg.Daemon.OutputRoot, "dreamer.log")

		text, err := readTail(path, maxBytes)
		if err != nil {
			if os.IsNotExist(err) {
				text = ""
			} else {
				http.Error(w, fmt.Sprintf("read log: %v", err), http.StatusInternalServerError)
				return
			}
		}

		if level := strings.TrimSpace(r.URL.Query().Get("level")); level != "" {
			text = filterLogLines(text, level)
		}

		if r.Header.Get("HX-Request") == "true" {
			// Log lines carry user-influenced fields (project paths,
			// provider stderr, finding text). Escape before injecting
			// into HTML so a stray "<script>" cannot execute under the
			// SPA's permissive CSP.
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, "<pre>")
			_, _ = io.WriteString(w, html.EscapeString(text))
			_, _ = io.WriteString(w, "</pre>")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, text)
	}
}

// readTail returns the last maxBytes of path, dropping the partial first line.
func readTail(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := fi.Size()
	start := int64(0)
	truncated := false
	if size > maxBytes {
		start = size - maxBytes
		truncated = true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	if truncated {
		// Drop the partial first line so tail starts on a complete one.
		if idx := bytes.IndexByte(buf, '\n'); idx >= 0 {
			buf = buf[idx+1:]
		}
	}
	return string(buf), nil
}

// filterLogLines keeps lines whose contents contain the level token
// (case-insensitive).
func filterLogLines(text, level string) string {
	lvl := strings.ToLower(level)
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.Contains(strings.ToLower(ln), lvl) {
			kept = append(kept, ln)
		}
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	return out
}
