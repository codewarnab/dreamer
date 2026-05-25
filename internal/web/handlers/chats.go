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
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
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

type chatsResponse struct {
	Sources []ChatSourceDTO `json:"sources"`
}

// ProjectChats handles GET (list), DELETE (single remove), and the
// :bulk-delete sub-route for /api/projects/{name}/chats. DELETE accepts
// {"path": "..."} in the JSON body. Both delete paths validate every target
// against the live DiscoverChats output so the endpoint cannot be used to
// delete arbitrary files via the path field.
func ProjectChats(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listProjectChats(deps, w, r)
		case http.MethodDelete:
			deleteProjectChat(deps, w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// ProjectChatsBulkDelete handles POST /api/projects/{name}/chats:bulk-delete.
// Body: {"paths": [...]}. Runs DiscoverChats once, validates each path,
// deletes the matching sources via their providers, and returns a per-path
// success/failure summary. Emits one chat.deleted SSE event per success.
func ProjectChatsBulkDelete(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		bulkDeleteProjectChats(deps, w, r)
	}
}

func listProjectChats(deps Deps, w http.ResponseWriter, r *http.Request) {
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
	project := findProjectByName(cfg, name)
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
	cutoff := time.Now().UTC().Add(-lookback)

	filtered := sources
	if toolFilter != "" {
		filtered = make([]chat.ChatSource, 0, len(sources))
		for _, source := range sources {
			if string(source.Tool) == toolFilter {
				filtered = append(filtered, source)
			}
		}
	}
	sizeByPath := batchSizesByPath(filtered)

	out := chatsResponse{Sources: make([]ChatSourceDTO, 0, len(filtered))}
	for _, source := range filtered {
		included := true
		if lookbackEnabled {
			included = !source.ModifiedTime.UTC().Before(cutoff)
		}
		size, ok := sizeByPath[source.Path]
		if !ok {
			size = singleSourceSize(source)
		}
		out.Sources = append(out.Sources, ChatSourceDTO{
			Tool:         string(source.Tool),
			Path:         source.Path,
			ModifiedUTC:  source.ModifiedTime.UTC().Format(time.RFC3339),
			MessageCount: -1,
			Included:     included,
			SizeBytes:    size,
		})
	}

	writeJSON(w, http.StatusOK, out)
}

// batchSizesByPath asks each provider that implements chat.BatchSizer for a
// bulk size lookup keyed by full source path. Single-source providers are
// resolved on demand by the caller. Sources are grouped by Tool first so each
// batch call sees only its own kind.
func batchSizesByPath(sources []chat.ChatSource) map[string]int64 {
	result := make(map[string]int64, len(sources))
	byTool := make(map[chat.SourceType][]chat.ChatSource)
	for _, source := range sources {
		byTool[source.Tool] = append(byTool[source.Tool], source)
	}
	for tool, group := range byTool {
		provider, ok := chat.ProviderFor(tool)
		if !ok {
			continue
		}
		if sizer, ok := provider.(chat.BatchSizer); ok {
			for path, size := range sizer.SizeBytesBatch(group) {
				result[path] = size
			}
		}
	}
	return result
}

// singleSourceSize falls back to the per-source SizeBytes call. Errors are
// swallowed: a single bad source should not blank the chats list.
func singleSourceSize(source chat.ChatSource) int64 {
	provider, ok := chat.ProviderFor(source.Tool)
	if !ok {
		return 0
	}
	size, err := provider.SizeBytes(source)
	if err != nil {
		return 0
	}
	return size
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
	project := findProjectByName(cfg, name)
	if project == nil {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	target := strings.TrimSpace(payload.Path)
	if target == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	sources, err := chat.DiscoverChats(project.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("discover chats: %v", err), http.StatusInternalServerError)
		return
	}
	found := findSourceByPath(sources, target)
	if found == nil {
		http.Error(w, "chat source not found for this project", http.StatusNotFound)
		return
	}

	if err := dispatchDelete(deps, name, *found); err != nil {
		http.Error(w, fmt.Sprintf("delete chat source: %v", err), http.StatusInternalServerError)
		return
	}
	dropChatHashEntries(deps, name, []string{found.Path})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": found.Path})
}

// bulkDeleteProjectChats runs DiscoverChats once, validates every requested
// path, deletes the matching sources, and reports per-path outcomes. Always
// returns 200 with a structured summary; transport-level errors are 4xx.
func bulkDeleteProjectChats(deps Deps, w http.ResponseWriter, r *http.Request) {
	cfg := deps.Config()
	if cfg == nil {
		http.Error(w, "config unavailable", http.StatusInternalServerError)
		return
	}
	name := bulkDeleteProjectName(r.URL.Path)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	project := findProjectByName(cfg, name)
	if project == nil {
		http.NotFound(w, r)
		return
	}

	var payload struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if len(payload.Paths) == 0 {
		http.Error(w, "paths required", http.StatusBadRequest)
		return
	}

	sources, err := chat.DiscoverChats(project.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("discover chats: %v", err), http.StatusInternalServerError)
		return
	}
	byPath := make(map[string]*chat.ChatSource, len(sources))
	for i := range sources {
		byPath[sources[i].Path] = &sources[i]
	}

	type result struct {
		Path  string `json:"path"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(payload.Paths))
	successCount := 0
	for _, raw := range payload.Paths {
		target := strings.TrimSpace(raw)
		if target == "" {
			results = append(results, result{Path: target, OK: false, Error: "empty path"})
			continue
		}
		source := byPath[target]
		if source == nil {
			results = append(results, result{Path: target, OK: false, Error: "not found"})
			continue
		}
		if err := dispatchDelete(deps, name, *source); err != nil {
			results = append(results, result{Path: target, OK: false, Error: err.Error()})
			continue
		}
		results = append(results, result{Path: target, OK: true})
		successCount++
	}
	if successCount > 0 {
		deletedPaths := make([]string, 0, successCount)
		for _, entry := range results {
			if entry.OK {
				deletedPaths = append(deletedPaths, entry.Path)
			}
		}
		dropChatHashEntries(deps, name, deletedPaths)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"requested": len(payload.Paths),
		"deleted":   successCount,
		"failed":    len(payload.Paths) - successCount,
		"results":   results,
	})
}

// dropChatHashEntries removes paths from state.ChatHashes and persists,
// restoring state.json as the single source of truth for "chats this
// project knows about" after a user-driven delete. Errors are logged but
// not propagated — the on-disk delete has already happened and we do not
// want to surface a stale-state error to the user; the next analyze run
// will reconcile.
//
// TODO: load→mutate→save is not serialized against the daemon's pipeline
// state writes (pipeline.go state.Save sites) or sibling lifecycle handlers
// (apply/dismiss/resolve). A concurrent daemon run can clobber these updates
// in its own Save. A package-level per-project mutex in internal/state used
// by every Load/Save caller would close the window.
func dropChatHashEntries(deps Deps, projectName string, paths []string) {
	if len(paths) == 0 {
		return
	}
	unlock := deps.StateLock.Lock(projectName)
	defer unlock()
	cfg := deps.Config()
	if cfg == nil {
		return
	}
	st, err := state.Load(cfg.Daemon.OutputRoot, projectName)
	if err != nil || st == nil {
		deps.Logger.Warn("load state after chat delete failed",
			logging.Any("project", projectName),
			logging.Any("err", err))
		return
	}
	changed := false
	for _, path := range paths {
		if _, ok := st.ChatHashes[path]; ok {
			delete(st.ChatHashes, path)
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := state.Save(cfg.Daemon.OutputRoot, projectName, st); err != nil {
		deps.Logger.Warn("save state after chat delete failed",
			logging.Any("project", projectName),
			logging.Any("err", err))
	}
}

// dispatchDelete looks up the provider for source.Tool, calls DeleteSource,
// logs the outcome (logger nil-safe), and publishes the chat.deleted event
// on success. Returned errors are caller-formatted.
func dispatchDelete(deps Deps, projectName string, source chat.ChatSource) error {
	provider, ok := chat.ProviderFor(source.Tool)
	if !ok {
		return fmt.Errorf("no provider for tool %q", source.Tool)
	}
	if err := provider.DeleteSource(source); err != nil {
		deps.Logger.Error("chat delete failed",
			logging.Any("project", projectName),
			logging.Any("tool", string(source.Tool)),
			logging.Any("path", source.Path),
			logging.Any("err", err))
		return err
	}
	deps.Logger.Info("chat deleted",
		logging.Any("project", projectName),
		logging.Any("tool", string(source.Tool)),
		logging.Any("path", source.Path))
	publish(deps.Events, pipeline.EventChatDeleted, map[string]any{
		"project": projectName,
		"tool":    string(source.Tool),
		"path":    source.Path,
	})
	return nil
}

func findProjectByName(cfg *config.Config, name string) *config.ProjectConfig {
	for i := range cfg.Projects {
		if cfg.Projects[i].Name == name {
			return &cfg.Projects[i]
		}
	}
	return nil
}

func findSourceByPath(sources []chat.ChatSource, target string) *chat.ChatSource {
	for i := range sources {
		if sources[i].Path == target {
			return &sources[i]
		}
	}
	return nil
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

// bulkDeleteProjectName extracts <name> from /api/projects/<name>/chats:bulk-delete.
func bulkDeleteProjectName(path string) string {
	const prefix = "/api/projects/"
	const suffix = "/chats:bulk-delete"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	name := strings.TrimPrefix(path, prefix)
	name = strings.TrimSuffix(name, suffix)
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
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
