package chat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(vscodeProvider{})
}

type vscodeProvider struct{}

func (vscodeProvider) Type() SourceType { return SourceTypeVSCodeChatSession }

func (vscodeProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverVSCodeChatSessions(env.AppDataDir, projectPath)
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
		}, skipDreamerMarkedFiles)
		if err != nil {
			return nil, err
		}
		discovered = append(discovered, workspaceChats...)
	}

	return discovered, nil
}

func (vscodeProvider) DeleteSource(source ChatSource) error {
	return deleteSourceFile(source.Path)
}

func (vscodeProvider) SizeBytes(source ChatSource) (int64, error) {
	return statSourceSize(source.Path)
}

func (vscodeProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".json", ".jsonl":
		messages, err := readers.ReadVSCodeChat(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read vscode chat source %q: %w", source.Path, err)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported vscode chat source file %q (supported: .json, .jsonl)", source.Path)
	}
}

// vscodeWorkspaceEvidenceKeys is the per-workspace key set VS Code uses to
// point at the project folder backing a workspaceStorage entry.
var vscodeWorkspaceEvidenceKeys = map[string]struct{}{
	"folder":       {},
	"workspace":    {},
	"path":         {},
	"uri":          {},
	"folderuri":    {},
	"workspaceuri": {},
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

	raw := recursiveExtract(document, vscodeWorkspaceEvidenceKeys, vscodeProbeMaxDepth)
	if raw == "" {
		return "", false
	}
	return decodeVSCodePath(raw), true
}

// decodeVSCodePath strips the file:// scheme and applies URL-decoding
// so percent-encoded characters (e.g. %20 for space, %3A for colon on
// Windows) round-trip correctly. On Windows, file:///C:/... has a
// leading slash before the drive letter — strip it.
func decodeVSCodePath(raw string) string {
	stripped := strings.TrimPrefix(raw, "file://")
	decoded, err := url.PathUnescape(stripped)
	if err != nil {
		decoded = stripped
	}
	// Windows: file:///C:/... -> /C:/... -> C:/...
	// Only strip when the second char is a drive letter (a-z or A-Z),
	// not for network paths like //server/share or Unix paths.
	if len(decoded) >= 3 && decoded[0] == '/' && decoded[2] == ':' &&
		((decoded[1] >= 'a' && decoded[1] <= 'z') || (decoded[1] >= 'A' && decoded[1] <= 'Z')) {
		decoded = decoded[1:]
	}
	return decoded
}
