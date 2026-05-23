package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

// ChatSourceDTO is the JSON payload entry for one discovered chat source.
type ChatSourceDTO struct {
	Tool         string `json:"tool"`
	Path         string `json:"path"`
	ModifiedUTC  string `json:"modified_utc"`
	MessageCount int    `json:"message_count"`
	Included     bool   `json:"included"`
	SizeBytes    int64  `json:"size_bytes"`
}

// chatSourceSizeBytes returns an approximate on-disk footprint for a chat
// source. File-backed sources use stat(); SQLite-backed sources sum the
// relevant blob columns for just the matching session/conversation.
// Errors return 0 so a single bad source does not break the chats list.
func chatSourceSizeBytes(src chat.ChatSource) int64 {
	switch src.Tool {
	case chat.SourceTypeOpenCodeSession:
		dbPath, sessionID := chat.SplitSQLiteSourcePath(src.Path)
		size, err := readers.OpenCodeReader{}.SessionSize(dbPath, sessionID)
		if err != nil {
			return 0
		}
		return size
	case chat.SourceTypeKiroCLISession:
		dbPath, conversationID := chat.SplitSQLiteSourcePath(src.Path)
		size, err := readers.KiroReader{}.ConversationSize(dbPath, conversationID)
		if err != nil {
			return 0
		}
		return size
	default:
		info, err := os.Stat(src.Path)
		if err != nil {
			return 0
		}
		return info.Size()
	}
}

type chatsResponse struct {
	Sources []ChatSourceDTO `json:"sources"`
}

// ProjectChats handles GET (list) and DELETE (remove) for
// /api/projects/{name}/chats. DELETE accepts {"path": "..."} in the JSON
// body and only removes sources that DiscoverChats currently surfaces for
// this project — guards against arbitrary FS writes via the path field.
func ProjectChats(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
		case http.MethodDelete:
			deleteProjectChat(deps, w, r)
			return
		default:
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
				SizeBytes:    chatSourceSizeBytes(src),
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// deleteProjectChat removes one discovered chat source. The target path is
// validated against the live DiscoverChats output so the endpoint cannot be
// used to delete arbitrary files.
func deleteProjectChat(deps Deps, w http.ResponseWriter, r *http.Request) {
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

	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(body.Path)
	if target == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	sources, err := chat.DiscoverChats(project.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("discover chats: %v", err), http.StatusInternalServerError)
		return
	}
	var match *chat.ChatSource
	for i := range sources {
		if sources[i].Path == target {
			match = &sources[i]
			break
		}
	}
	if match == nil {
		http.Error(w, "chat source not found for this project", http.StatusNotFound)
		return
	}

	provider, ok := chat.ProviderFor(match.Tool)
	if !ok {
		http.Error(w, fmt.Sprintf("no provider for tool %q", match.Tool), http.StatusInternalServerError)
		return
	}
	if err := provider.DeleteSource(*match); err != nil {
		if deps.Logger != nil {
			deps.Logger.Error("chat delete failed",
				logging.Any("project", name),
				logging.Any("tool", string(match.Tool)),
				logging.Any("path", match.Path),
				logging.Any("err", err))
		}
		http.Error(w, fmt.Sprintf("delete chat source: %v", err), http.StatusInternalServerError)
		return
	}
	if deps.Logger != nil {
		deps.Logger.Info("chat deleted",
			logging.Any("project", name),
			logging.Any("tool", string(match.Tool)),
			logging.Any("path", match.Path))
	}

	publish(deps.Events, pipeline.EventChatDeleted, map[string]any{
		"project": name,
		"tool":    string(match.Tool),
		"path":    match.Path,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": match.Path})
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
	return pipeline.ParseLookbackDuration(amount, unitText)
}
