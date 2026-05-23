package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(codexProvider{})
}

type codexProvider struct{}

func (codexProvider) Type() SourceType { return SourceTypeCodexSessionJSONL }

func (codexProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverCodexSessions(env.HomeDir, projectPath)
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
		}, skipDreamerMarkedFiles)
		if err != nil {
			return nil, err
		}
		for _, source := range sources {
			if _, ok := seen[source.Path]; ok {
				continue
			}
			candidateCWD, ok := probeCodexSessionCWD(source.Path)
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

func (codexProvider) DeleteSource(source ChatSource) error {
	return deleteSourceFile(source.Path)
}

func (codexProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	messages, err := readers.ReadJSONLWithOptions(source.Path, readers.JSONLReadOptions{
		Sanitizer: readers.SanitizeCodexMessages,
	})
	if err != nil {
		return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
	}
	return messages, nil
}

func probeCodexSessionCWD(sessionPath string) (string, bool) {
	return probeJSONLForCWD(sessionPath, probeLineLimit, extractCodexCWD)
}

// extractCodexCWD walks one decoded JSONL record looking for the
// session_meta.payload.cwd evidence. Codex emits two shapes:
//   - {"session_meta": {"payload": {"cwd": "..."}}}
//   - {"type": "session_meta", "payload": {"cwd": "..."}}
func extractCodexCWD(record map[string]any) string {
	return walkCodexForCWD(record, 0)
}

func walkCodexForCWD(value any, depth int) string {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		if sessionMeta, ok := valueForNormalizedKey(typed, "sessionmeta"); ok {
			if cwd := extractCodexPayloadCWD(sessionMeta, depth+1); cwd != "" {
				return cwd
			}
		}

		if recordType, ok := stringValueForNormalizedKey(typed, "type"); ok && normalizeDiscoveryKey(recordType) == "sessionmeta" {
			if payload, ok := valueForNormalizedKey(typed, "payload"); ok {
				if cwd := extractCodexPayloadCWD(payload, depth+1); cwd != "" {
					return cwd
				}
			}
		}

		for _, nested := range typed {
			if cwd := walkCodexForCWD(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	case []any:
		for _, nested := range typed {
			if cwd := walkCodexForCWD(nested, depth+1); cwd != "" {
				return cwd
			}
		}
	}

	return ""
}

func extractCodexPayloadCWD(value any, depth int) string {
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
		return extractCodexPayloadCWD(payload, depth+1)
	}

	return ""
}
