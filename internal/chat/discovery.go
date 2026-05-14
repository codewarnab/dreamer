package chat

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"dreamer/internal/chat/readers"
)

// sqliteReaderAvailable reports whether the discovery layer should attempt to
// open a SQLite-backed chat source. When the caller provided a custom Open
// hook or a non-default driver name, discovery trusts them. Otherwise it
// requires the default sqlite3 driver to be registered in the process — this
// keeps discovery silent in builds that have not linked a sqlite driver.
func sqliteReaderAvailable(driverName string, openHook func(string, string) (*sql.DB, error)) bool {
	if openHook != nil {
		return true
	}
	if trimmed := strings.TrimSpace(driverName); trimmed != "" && trimmed != defaultSQLiteDriverName {
		return true
	}
	for _, name := range sql.Drivers() {
		if name == defaultSQLiteDriverName {
			return true
		}
	}
	return false
}

// defaultSQLiteDriverName mirrors the value from readers.SQLiteReader so the
// availability check stays in one place. Kept private to the package.
const defaultSQLiteDriverName = "sqlite"

type SourceType string

const (
	SourceTypeCopilotSessionJSONL SourceType = "copilot-session-jsonl"
	SourceTypeCodexSessionJSONL   SourceType = "codex-session-jsonl"
	SourceTypeVSCodeChatSession   SourceType = "vscode-chat-session"
	SourceTypeClaudeCodeSession   SourceType = "claude-code-session-jsonl"
	SourceTypeAntigravityGemini   SourceType = "antigravity-gemini-session"
	SourceTypeGeminiCLISession    SourceType = "gemini-cli-session-jsonl"
	SourceTypeOpenCodeSession     SourceType = "opencode-session-sqlite"
	SourceTypeKiroCLISession      SourceType = "kiro-cli-session-sqlite"
)

// sqliteSourcePathSeparator separates the database file path from the
// session/conversation identifier when a ChatSource refers to a single row
// inside a shared SQLite database (opencode, kiro-cli). Discovery encodes
// `<dbPath>#<sessionID>`; the runtime reader splits on this separator.
const sqliteSourcePathSeparator = "#"

// SplitSQLiteSourcePath returns (dbPath, sessionID) for a ChatSource path that
// was produced by SQLite-backed discovery. When the path has no separator the
// raw path is returned with an empty session id.
func SplitSQLiteSourcePath(path string) (string, string) {
	index := strings.LastIndex(path, sqliteSourcePathSeparator)
	if index < 0 {
		return path, ""
	}
	return path[:index], path[index+1:]
}

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

// DiscoveryEnvironment captures the OS-derived inputs that drive chat source
// discovery. Tests build this manually; DiscoverChats resolves it from the
// process environment.
type DiscoveryEnvironment struct {
	HomeDir         string
	AppDataDir      string
	DataHomeDir     string
	ClaudeConfigDir string
	GeminiHomeDir   string
	OpenCodeDBPath  string
	KiroCLIDBPath   string
	OpenCodeReader  readers.OpenCodeReader
	KiroReader      readers.KiroReader
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

	dataHomeDir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHomeDir == "" {
		dataHomeDir = filepath.Join(homeDir, ".local", "share")
	}

	environment := DiscoveryEnvironment{
		HomeDir:         homeDir,
		AppDataDir:      appDataDir,
		DataHomeDir:     dataHomeDir,
		ClaudeConfigDir: strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")),
		GeminiHomeDir:   strings.TrimSpace(os.Getenv("GEMINI_HOME")),
		OpenCodeDBPath:  strings.TrimSpace(os.Getenv("OPENCODE_DB")),
		KiroCLIDBPath:   strings.TrimSpace(os.Getenv("KIRO_CLI_DB")),
	}

	return discoverChatsFromEnvironment(environment, projectPath)
}

func discoverChatsFromRoots(homeDir string, appDataDir string, claudeConfigDir string, projectPath string, geminiHomeDir string) ([]ChatSource, error) {
	return discoverChatsFromEnvironment(DiscoveryEnvironment{
		HomeDir:         homeDir,
		AppDataDir:      appDataDir,
		DataHomeDir:     filepath.Join(strings.TrimSpace(homeDir), ".local", "share"),
		ClaudeConfigDir: claudeConfigDir,
		GeminiHomeDir:   geminiHomeDir,
	}, projectPath)
}

func discoverChatsFromEnvironment(environment DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	homeDir := environment.HomeDir
	appDataDir := environment.AppDataDir
	claudeConfigDir := environment.ClaudeConfigDir
	geminiHomeDir := environment.GeminiHomeDir
	copilotSources, err := discoverCopilotSessionState(homeDir)
	if err != nil {
		return nil, err
	}

	codexSources, err := discoverCodexSessions(homeDir, projectPath)
	if err != nil {
		return nil, err
	}

	vscodeSources, err := discoverVSCodeChatSessions(appDataDir, projectPath)
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

	geminiCLISources, err := discoverGeminiCLISessions(homeDir, geminiHomeDir, projectPath)
	if err != nil {
		return nil, err
	}

	openCodeSources, err := discoverOpenCodeSessions(environment, projectPath)
	if err != nil {
		return nil, err
	}

	kiroCLISources, err := discoverKiroCLISessions(environment, projectPath)
	if err != nil {
		return nil, err
	}

	combined := append(copilotSources, codexSources...)
	combined = append(combined, vscodeSources...)
	combined = append(combined, claudeSources...)
	combined = append(combined, antigravitySources...)
	combined = append(combined, geminiCLISources...)
	combined = append(combined, openCodeSources...)
	combined = append(combined, kiroCLISources...)
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

func discoverVSCodeChatSessions(appDataDir string, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

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

		workspaceRoot := filepath.Join(workspaceStorageRoot, workspaceEntry.Name())
		workspaceEvidence, ok := readVSCodeWorkspaceEvidence(filepath.Join(workspaceRoot, "workspace.json"))
		if !ok {
			continue
		}
		normalizedEvidence, ok := normalizeDiscoveryEvidencePath(workspaceEvidence)
		if !ok || !pathWithinNormalizedRoot(normalizedEvidence, normalizedProjectPath) {
			continue
		}

		chatRoot := filepath.Join(workspaceRoot, "chatSessions")
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

func readVSCodeWorkspaceEvidence(workspaceJSONPath string) (string, bool) {
	payload, err := os.ReadFile(workspaceJSONPath)
	if err != nil {
		return "", false
	}

	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return "", false
	}

	workspacePath := extractVSCodeWorkspacePath(document, 0)
	return workspacePath, workspacePath != ""
}

func extractVSCodeWorkspacePath(value any, depth int) string {
	const maxDepth = 6

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"folder", "workspace", "path", "uri", "folderUri", "workspaceUri"} {
			if path := extractVSCodeWorkspacePath(typed[key], depth+1); path != "" {
				return strings.TrimPrefix(path, "file://")
			}
		}
	case []any:
		for _, nested := range typed {
			if path := extractVSCodeWorkspacePath(nested, depth+1); path != "" {
				return path
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed != "" {
			return strings.TrimPrefix(trimmed, "file://")
		}
	}

	return ""
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
	type antigravityRoot struct {
		path          string
		requiresProbe bool
	}

	roots := make([]antigravityRoot, 0, 3)

	trimmedGeminiHome := strings.TrimSpace(geminiHomeDir)
	if trimmedGeminiHome != "" {
		roots = append(roots, antigravityRoot{path: filepath.Join(trimmedGeminiHome, "antigravity"), requiresProbe: true})
	} else {
		roots = append(roots, antigravityRoot{path: filepath.Join(strings.TrimSpace(homeDir), ".gemini", "antigravity"), requiresProbe: true})
	}

	trimmedProjectPath := strings.TrimSpace(projectPath)
	if trimmedProjectPath != "" {
		roots = append(roots, antigravityRoot{path: filepath.Join(trimmedProjectPath, ".gemini", "antigravity")})
	}

	normalizedProjectPath, hasProjectPath := normalizeDiscoveryPath(projectPath)
	if !hasProjectPath {
		return nil, nil
	}

	discovered := make([]ChatSource, 0)
	seen := map[string]struct{}{}
	for _, root := range roots {
		for _, conversationsDir := range []string{"conversations", "inbox"} {
			sources, err := walkChatFiles(filepath.Join(root.path, conversationsDir), SourceTypeAntigravityGemini, map[string]struct{}{
				".pb":    {},
				".pbtxt": {},
				".jsonl": {},
			})
			if err != nil {
				return nil, err
			}
			for _, source := range sources {
				if _, ok := seen[source.Path]; ok {
					continue
				}
				if root.requiresProbe && !antigravitySourceBelongsToProject(source.Path, normalizedProjectPath) {
					continue
				}
				seen[source.Path] = struct{}{}
				discovered = append(discovered, source)
			}
		}
	}

	return discovered, nil
}

func antigravitySourceBelongsToProject(sourcePath string, normalizedProjectPath string) bool {
	candidatePath, ok := probeAntigravityProjectEvidence(sourcePath)
	if !ok {
		return false
	}
	normalizedCandidatePath, ok := normalizeDiscoveryEvidencePath(candidatePath)
	if !ok {
		return false
	}
	return pathWithinNormalizedRoot(normalizedCandidatePath, normalizedProjectPath)
}

func probeAntigravityProjectEvidence(sourcePath string) (string, bool) {
	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", false
	}

	if path, ok := probeAntigravityJSONEvidence(payload); ok {
		return path, true
	}
	return probeAntigravityTextEvidence(string(payload))
}

func probeAntigravityJSONEvidence(payload []byte) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	scanner.Buffer(make([]byte, claudeProbeInitialBufferSize), claudeProbeMaxBufferSize)

	linesRead := 0
	for scanner.Scan() {
		linesRead++
		if linesRead > claudeProbeLineLimit {
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
		if path := extractClaudeCWDEvidence(record, 0); path != "" {
			return path, true
		}
	}

	return "", false
}

func probeAntigravityTextEvidence(content string) (string, bool) {
	linesRead := 0
	for _, rawLine := range strings.Split(content, "\n") {
		linesRead++
		if linesRead > claudeProbeLineLimit {
			break
		}

		key, value, ok := splitDiscoveryField(rawLine)
		if !ok {
			continue
		}
		if _, ok := claudeCWDEvidenceKeys[normalizeDiscoveryKey(key)]; ok {
			if path := strings.TrimSpace(value); path != "" {
				return path, true
			}
		}
	}

	return "", false
}

func splitDiscoveryField(line string) (string, string, bool) {
	index := strings.Index(line, ":")
	if index <= 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index+1:])
	value = strings.Trim(value, `"`)
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
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

func discoverGeminiCLISessions(homeDir string, geminiHomeDir string, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}

	geminiRoot := strings.TrimSpace(geminiHomeDir)
	if geminiRoot == "" {
		geminiRoot = filepath.Join(strings.TrimSpace(homeDir), ".gemini")
	}

	root := filepath.Join(geminiRoot, "tmp")
	candidates, err := walkChatFiles(root, SourceTypeGeminiCLISession, map[string]struct{}{
		".jsonl": {},
		".json":  {},
	})
	if err != nil {
		return nil, err
	}

	discovered := make([]ChatSource, 0, len(candidates))
	for _, candidate := range candidates {
		if !strings.Contains(candidate.Path, string(filepath.Separator)+"chats"+string(filepath.Separator)) {
			continue
		}
		candidateCWD, ok := probeGeminiCLISessionCWD(candidate.Path, claudeProbeLineLimit)
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

func probeGeminiCLISessionCWD(sessionPath string, maxLines int) (string, bool) {
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
		if directories, ok := record["directories"].([]any); ok {
			for _, value := range directories {
				if path := extractPathValue(value, 0); path != "" {
					return path, true
				}
			}
		}
		if path := extractClaudeCWDEvidence(record, 0); path != "" {
			return path, true
		}
	}
	return "", false
}

func discoverOpenCodeSessions(environment DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}
	if !sqliteReaderAvailable(environment.OpenCodeReader.DriverName, environment.OpenCodeReader.Open) {
		return nil, nil
	}

	dbPath := strings.TrimSpace(environment.OpenCodeDBPath)
	if dbPath == "" {
		dbPath = filepath.Join(strings.TrimSpace(environment.DataHomeDir), "opencode", "opencode.db")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat opencode database %q: %w", dbPath, err)
	}

	sessions, err := environment.OpenCodeReader.ListSessions(dbPath, "")
	if err != nil {
		return nil, fmt.Errorf("list opencode sessions: %w", err)
	}

	discovered := make([]ChatSource, 0, len(sessions))
	for _, session := range sessions {
		normalizedDir, ok := normalizeDiscoveryEvidencePath(session.Directory)
		if !ok {
			continue
		}
		if !pathWithinNormalizedRoot(normalizedDir, normalizedProjectPath) {
			continue
		}
		discovered = append(discovered, ChatSource{
			Path:         dbPath + sqliteSourcePathSeparator + session.ID,
			Tool:         SourceTypeOpenCodeSession,
			ModifiedTime: session.ModifiedTime,
		})
	}
	return discovered, nil
}

func discoverKiroCLISessions(environment DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	normalizedProjectPath, ok := normalizeDiscoveryPath(projectPath)
	if !ok {
		return nil, nil
	}
	if !sqliteReaderAvailable(environment.KiroReader.DriverName, environment.KiroReader.Open) {
		return nil, nil
	}

	dbPath := strings.TrimSpace(environment.KiroCLIDBPath)
	if dbPath == "" {
		dbPath = filepath.Join(strings.TrimSpace(environment.DataHomeDir), "kiro-cli", "data.sqlite3")
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat kiro database %q: %w", dbPath, err)
	}

	conversations, err := environment.KiroReader.ListConversations(dbPath, "")
	if err != nil {
		return nil, fmt.Errorf("list kiro conversations: %w", err)
	}

	discovered := make([]ChatSource, 0, len(conversations))
	for _, conversation := range conversations {
		normalizedDir, ok := normalizeDiscoveryEvidencePath(conversation.Directory)
		if !ok {
			continue
		}
		if !pathWithinNormalizedRoot(normalizedDir, normalizedProjectPath) {
			continue
		}
		discovered = append(discovered, ChatSource{
			Path:         dbPath + sqliteSourcePathSeparator + conversation.ConversationID,
			Tool:         SourceTypeKiroCLISession,
			ModifiedTime: conversation.ModifiedTime,
		})
	}
	return discovered, nil
}
