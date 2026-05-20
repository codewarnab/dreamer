package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/chat"
	"dreamer/internal/config"
)

// ChatSourceDTO is the JSON payload entry for one discovered chat source.
type ChatSourceDTO struct {
	Tool         string `json:"tool"`
	Path         string `json:"path"`
	ModifiedUTC  string `json:"modified_utc"`
	MessageCount int    `json:"message_count"`
	Included     bool   `json:"included"`
}

type chatsResponse struct {
	Sources []ChatSourceDTO `json:"sources"`
}

// ProjectChats returns GET /api/projects/{name}/chats — the chat-source
// inventory chat.DiscoverChats reports for the project, decorated with
// whether each source's modified time falls inside the project's `since`
// lookback window. Supports optional ?tool=<source-type> filtering.
func ProjectChats(deps Deps) http.HandlerFunc {
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
		name := chatsProjectName(r.URL.Path)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		var project *config.ProjectConfig
		for i := range cfg.Projects {
			if cfg.Projects[i].Name == name {
				project = &cfg.Projects[i]
				break
			}
		}
		if project == nil {
			http.NotFound(w, r)
			return
		}

		sources, err := chat.DiscoverChats(project.Path)
		if err != nil {
			http.Error(w, fmt.Sprintf("discover chats: %v", err), http.StatusInternalServerError)
			return
		}

		toolFilter := strings.TrimSpace(r.URL.Query().Get("tool"))
		lookback, lookbackEnabled := parseSinceWindow(project.Since)
		now := time.Now().UTC()
		cutoff := now.Add(-lookback)

		out := chatsResponse{Sources: make([]ChatSourceDTO, 0, len(sources))}
		for _, src := range sources {
			if toolFilter != "" && string(src.Tool) != toolFilter {
				continue
			}
			included := true
			if lookbackEnabled {
				included = !src.ModifiedTime.UTC().Before(cutoff)
			}
			out.Sources = append(out.Sources, ChatSourceDTO{
				Tool:         string(src.Tool),
				Path:         src.Path,
				ModifiedUTC:  src.ModifiedTime.UTC().Format(time.RFC3339),
				MessageCount: -1,
				Included:     included,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// chatsProjectName extracts <name> from /api/projects/<name>/chats. Returns
// empty when the path does not match.
func chatsProjectName(path string) string {
	const prefix = "/api/projects/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	tail := strings.TrimPrefix(path, prefix)
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || parts[1] != "chats" || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// parseSinceWindow parses a project's `since` value into a duration. An empty
// string, "lifetime", or unparseable input returns enabled=false meaning the
// lookback filter is disabled and every source is treated as included.
// Supported units mirror pipeline.parseLookbackWindow: m, h, d, w, mo.
func parseSinceWindow(value string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || config.IsLifetimeSince(trimmed) {
		return 0, false
	}
	var amountText, unitText string
	if strings.HasSuffix(trimmed, "mo") {
		amountText, unitText = trimmed[:len(trimmed)-2], "mo"
	} else if len(trimmed) >= 2 {
		amountText, unitText = trimmed[:len(trimmed)-1], trimmed[len(trimmed)-1:]
	} else {
		return 0, false
	}
	amount, err := strconv.Atoi(amountText)
	if err != nil || amount <= 0 {
		return 0, false
	}
	switch unitText {
	case "m":
		return time.Duration(amount) * time.Minute, true
	case "h":
		return time.Duration(amount) * time.Hour, true
	case "d":
		return time.Duration(amount) * 24 * time.Hour, true
	case "w":
		return time.Duration(amount) * 7 * 24 * time.Hour, true
	case "mo":
		return time.Duration(amount) * 30 * 24 * time.Hour, true
	}
	return 0, false
}
