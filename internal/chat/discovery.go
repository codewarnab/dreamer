package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type SourceType string

const (
	SourceTypeCopilotSessionJSONL SourceType = "copilot-session-jsonl"
	SourceTypeCodexSessionJSONL   SourceType = "codex-session-jsonl"
	SourceTypeVSCodeChatSession   SourceType = "vscode-chat-session"
	SourceTypeClaudeCodeSession   SourceType = "claude-code-session-jsonl"
	SourceTypeAntigravityGemini   SourceType = "antigravity-gemini-session"
)

const (
	claudeProbeLineLimit         = 200
	claudeProbeInitialBufferSize = 64 * 1024
	claudeProbeMaxBufferSize     = 8 * 1024 * 1024
	codexProbeLineLimit          = 200
)

var claudeCWDEvidenceKeys = map[string]struct{}{
	"cwd":                     {},
	"currentworkingdirectory": {},
	"workingdirectory":        {},
	"workdir":                 {},
	"projectpath":             {},
	"workspacepath":           {},
	"workspacefolder":         {},
	"rootpath":                {},
	"reporoot":                {},
	"repositoryroot":          {},
}

type ChatSource struct {
	Path         string
	Tool         SourceType
	ModifiedTime time.Time
}

func DiscoverChats(projectPath string) ([]ChatSource, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home: %w", err)
	}

	appDataDir := strings.TrimSpace(os.Getenv("APPDATA"))
	if appDataDir == "" {
		appDataDir = filepath.Join(homeDir, "AppData", "Roaming")
	}

	claudeConfigDir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	geminiHomeDir := strings.TrimSpace(os.Getenv("GEMINI_HOME"))

	return discoverChatsFromRoots(homeDir, appDataDir, claudeConfigDir, projectPath, geminiHomeDir)
}

func discoverChatsFromRoots(homeDir string, appDataDir string, claudeConfigDir string, projectPath string, geminiHomeDir string) ([]ChatSource, error) {
	copilotSources, err := discoverCopilotSessionState(homeDir)
	if err != nil {
		return nil, err
	}

	codexSources, err := discoverCodexSessions(homeDir, projectPath)
	if err != nil {
		return nil, err
	}

	vscodeSources, err := discoverVSCodeChatSessions(appDataDir)
	if err != nil {
		return nil, err
	}

	claudeSources, err := discoverClaudeCodeSessions(homeDir, claudeConfigDir, projectPath)
	if err != nil {
		return nil, err
	}

	antigravitySources, err := discoverAntigravityGeminiSessions(homeDir, projectPath, geminiHomeDir)
	if err != nil {
		return nil, err
	}

	combined := append(copilotSources, codexSources...)
	combined = append(combined, vscodeSources...)
	combined = append(combined, claudeSources...)
	combined = append(combined, antigravitySources...)
	sort.Slice(combined, func(i int, j int) bool {
		left := combined[i]
		right := combined[j]
		if left.ModifiedTime.Equal(right.ModifiedTime) {
			return left.Path < right.Path
		}
		return left.ModifiedTime.After(right.ModifiedTime)
	})

	return combined, nil
}

func discoverCopilotSessionState(homeDir string) ([]ChatSource, error) {
	root := filepath.Join(strings.TrimSpace(homeDir), ".copilot", "session-state")
	return walkChatFiles(root, SourceTypeCopilotSessionJSONL, map[string]struct{}{
		".jsonl": {},
	})
}

func discoverCodexSessions(homeDir string, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

	codexRoot := filepath.Join(strings.TrimSpace(homeDir), ".codex")
	discovered := make([]ChatSource, 0)
	seen := map[string]struct{}{}
	for _, root := range []string{
		filepath.Join(codexRoot, "sessions"),
		filepath.Join(codexRoot, "archived_sessions"),
	} {
		sources, err := walkChatFiles(root, SourceTypeCodexSessionJSONL, map[string]struct{}{
			".jsonl": {},
		})
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			if _, ok := seen[source.Path]; ok {
				continue
			}
			candidateCWD, ok := probeCodexSessionCWD(source.Path, codexProbeLineLimit)
			if !ok {
				continue
			}
			normalizedCandidateCWD, ok := normalizeDiscoveryEvidencePath(candidateCWD)
			if !ok {
				continue
			}
			if !pathWithinNormalizedRoot(normalizedCandidateCWD, normalizedProjectPath) {
				continue
			}
			seen[source.Path] = struct{}{}
			discovered = append(discovered, source)
		}
	}
	return discovered, nil
}

func discoverVSCodeChatSessions(appDataDir string) ([]ChatSource, error) {
	workspaceStorageRoot := filepath.Join(strings.TrimSpace(appDataDir), "Code", "User", "workspaceStorage")
	info, err := os.Stat(workspaceStorageRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat vscode workspace storage %q: %w", workspaceStorageRoot, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	workspaceEntries, err := os.ReadDir(workspaceStorageRoot)
	if err != nil {
		return nil, fmt.Errorf("read vscode workspace storage %q: %w", workspaceStorageRoot, err)
	}

	discovered := make([]ChatSource, 0)
	for _, workspaceEntry := range workspaceEntries {
		if !workspaceEntry.IsDir() {
			continue
		}

		chatRoot := filepath.Join(workspaceStorageRoot, workspaceEntry.Name(), "chatSessions")
		workspaceChats, err := walkChatFiles(chatRoot, SourceTypeVSCodeChatSession, map[string]struct{}{
			".json":  {},
			".jsonl": {},
		})
		if err != nil {
			return nil, err
		}
		discovered = append(discovered, workspaceChats...)
	}

	return discovered, nil
}

func discoverClaudeCodeSessions(homeDir string, claudeConfigDir string, projectPath string) ([]ChatSource, error) {
	claudeRoot := strings.TrimSpace(claudeConfigDir)
	if claudeRoot == "" {
		claudeRoot = filepath.Join(strings.TrimSpace(homeDir), ".claude")
	}

	root := filepath.Join(claudeRoot, "projects")
	candidates, err := walkChatFiles(root, SourceTypeClaudeCodeSession, map[string]struct{}{
		".jsonl": {},
	})
	if err != nil {
		return nil, err
	}

	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

	discovered := make([]ChatSource, 0, len(candidates))
	for _, candidate := range candidates {
		candidateCWD, ok := probeClaudeSessionCWD(candidate.Path, claudeProbeLineLimit)
		if !ok {
			continue
		}
		normalizedCandidateCWD, ok := normalizeDiscoveryEvidencePath(candidateCWD)
		if !ok {
			continue
		}
		if pathWithinNormalizedRoot(normalizedCandidateCWD, normalizedProjectPath) {
			discovered = append(discovered, candidate)
		}
	}

	return discovered, nil
}

func discoverAntigravityGeminiSessions(homeDir string, projectPath string, geminiHomeDir string) ([]ChatSource, error) {
	roots := make([]string, 0, 3)

	trimmedGeminiHome := strings.TrimSpace(geminiHomeDir)
	if trimmedGeminiHome != "" {
		roots = append(roots, filepath.Join(trimmedGeminiHome, "antigravity"))
	} else {
		roots = append(roots, filepath.Join(strings.TrimSpace(homeDir), ".gemini", "antigravity"))
	}

	trimmedProjectPath := strings.TrimSpace(projectPath)
	if trimmedProjectPath != "" {
		roots = append(roots, filepath.Join(trimmedProjectPath, ".gemini", "antigravity"))
	}

	discovered := make([]ChatSource, 0)
	seen := map[string]struct{}{}
	for _, root := range roots {
		for _, conversationsDir := range []string{"conversations", "inbox"} {
			sources, err := walkChatFiles(filepath.Join(root, conversationsDir), SourceTypeAntigravityGemini, map[string]struct{}{
				".pb":    {},
				".pbtxt": {},
				".json":  {},
				".jsonl": {},
			})
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				if _, ok := seen[source.Path]; ok {
					continue
				}
				seen[source.Path] = struct{}{}
				discovered = append(discovered, source)
			}
		}
	}

	return discovered, nil
}

func walkChatFiles(root string, sourceType SourceType, extensions map[string]struct{}) ([]ChatSource, error) {
	trimmedRoot := strings.TrimSpace(root)
	if trimmedRoot == "" {
		return nil, nil
	}

	info, err := os.Stat(trimmedRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat chat root %q: %w", trimmedRoot, err)
	}
	if !info.IsDir() {
		return nil, nil
	}

	discovered := make([]ChatSource, 0)
	err = filepath.WalkDir(trimmedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}

		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if _, ok := extensions[extension]; !ok {
			return nil
		}

		fileInfo, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read chat file metadata for %q: %w", path, err)
		}

		discovered = append(discovered, ChatSource{
			Path:         path,
			Tool:         sourceType,
			ModifiedTime: fileInfo.ModTime().UTC(),
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk chat root %q: %w", trimmedRoot, err)
	}

	return discovered, nil
}

func probeCodexSessionCWD(sessionPath string, maxLines int) (string, bool) {
	file, err := os.Open(sessionPath)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, claudeProbeInitialBufferSize), claudeProbeMaxBufferSize)

	linesRead := 0
	for scanner.Scan() {
		linesRead++
		if maxLines > 0 && linesRead > maxLines {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		if cwd := extractCodexSessionMetaPayloadCWD(record, 0); cwd != "" {
			return cwd, true
		}
	}

	if err := scanner.Err(); err != nil {
		return "", false
	}

	return "", false
}

func extractCodexSessionMetaPayloadCWD(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		if sessionMeta, ok := valueForNormalizedKey(typed, "sessionmeta"); ok {
			if cwd := extractCodexSessionMetaPayload(sessionMeta, depth+1); cwd != "" {
				return cwd
			}
		}

		if recordType, ok := stringValueForNormalizedKey(typed, "type"); ok && normalizeDiscoveryKey(recordType) == "sessionmeta" {
			if payload, ok := valueForNormalizedKey(typed, "payload"); ok {
				if cwd := extractCodexSessionMetaPayload(payload, depth+1); cwd != "" {
					return cwd
				}
			}
		}

		for _, nested := range typed {
			if cwd := extractCodexSessionMetaPayloadCWD(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	case []any:
		for _, nested := range typed {
			if cwd := extractCodexSessionMetaPayloadCWD(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	}

	return ""
}

func extractCodexSessionMetaPayload(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	record, ok := value.(map[string]any)
	if !ok {
		return ""
	}

	if cwdValue, ok := valueForNormalizedKey(record, "cwd"); ok {
		return extractPathValue(cwdValue, depth+1)
	}

	if payload, ok := valueForNormalizedKey(record, "payload"); ok {
		return extractCodexSessionMetaPayload(payload, depth+1)
	}

	return ""
}

func valueForNormalizedKey(record map[string]any, normalizedKey string) (any, bool) {
	for key, value := range record {
		if normalizeDiscoveryKey(key) == normalizedKey {
			return value, true
		}
	}
	return nil, false
}

func stringValueForNormalizedKey(record map[string]any, normalizedKey string) (string, bool) {
	value, ok := valueForNormalizedKey(record, normalizedKey)
	if !ok {
		return "", false
	}

	typed, ok := value.(string)
	if !ok {
		return "", false
	}

	trimmed := strings.TrimSpace(typed)
	if trimmed == "" {
		return "", false
	}

	return trimmed, true
}

func probeClaudeSessionCWD(sessionPath string, maxLines int) (string, bool) {
	file, err := os.Open(sessionPath)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, claudeProbeInitialBufferSize), claudeProbeMaxBufferSize)

	linesRead := 0
	for scanner.Scan() {
		linesRead++
		if maxLines > 0 && linesRead > maxLines {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		if cwd := extractClaudeCWDEvidence(record, 0); cwd != "" {
			return cwd, true
		}
	}

	if err := scanner.Err(); err != nil {
		return "", false
	}

	return "", false
}

func extractClaudeCWDEvidence(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, ok := claudeCWDEvidenceKeys[normalizeDiscoveryKey(key)]; ok {
				if path := extractPathValue(nested, depth+1); path != "" {
					return path
				}
			}
		}
		for _, nested := range typed {
			if cwd := extractClaudeCWDEvidence(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	case []any:
		for _, nested := range typed {
			if cwd := extractClaudeCWDEvidence(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	}

	return ""
}

func extractPathValue(value any, depth int) string {
	const maxDepth = 6

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case map[string]any:
		for _, key := range []string{"path", "value", "cwd", "currentWorkingDirectory", "workingDirectory", "projectPath", "workspacePath", "root"} {
			if path := extractPathValue(typed[key], depth+1); path != "" {
				return path
			}
		}
	case []any:
		for _, nested := range typed {
			if path := extractPathValue(nested, depth+1); path != "" {
				return path
			}
		}
	}

	return ""
}

func normalizeDiscoveryKey(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
}

func normalizeDiscoveryPath(raw string) (string, bool) {
	return normalizeDiscoveryPathWithOptions(raw, false)
}

func normalizeDiscoveryEvidencePath(raw string) (string, bool) {
	return normalizeDiscoveryPathWithOptions(raw, true)
}

func normalizeDiscoveryPathWithOptions(raw string, requireAbsolute bool) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	normalizedSeparators := strings.ReplaceAll(trimmed, `\`, "/")
	path := filepath.Clean(filepath.FromSlash(normalizedSeparators))
	if requireAbsolute && !filepath.IsAbs(path) {
		return "", false
	}

	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		var err error
		absolutePath, err = filepath.Abs(path)
		if err != nil {
			return "", false
		}
	}

	cleanPath := filepath.Clean(absolutePath)
	resolvedPath, err := filepath.EvalSymlinks(cleanPath)
	if err == nil {
		cleanPath = filepath.Clean(resolvedPath)
	}

	return cleanPath, true
}

func pathWithinNormalizedRoot(path string, root string) bool {
	normalizedPath := normalizeDiscoveryPathForComparison(path)
	normalizedRoot := normalizeDiscoveryPathForComparison(root)
	if normalizedPath == "" || normalizedRoot == "" {
		return false
	}

	if normalizedPath == normalizedRoot {
		return true
	}

	rootWithSeparator := normalizedRoot
	if !strings.HasSuffix(rootWithSeparator, string(filepath.Separator)) {
		rootWithSeparator += string(filepath.Separator)
	}
	return strings.HasPrefix(normalizedPath, rootWithSeparator)
}

func normalizeDiscoveryPathForComparison(path string) string {
	normalizedSeparators := strings.ReplaceAll(path, `\`, "/")
	cleanPath := filepath.Clean(filepath.FromSlash(normalizedSeparators))
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleanPath)
	}
	return cleanPath
}
