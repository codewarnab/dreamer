package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(antigravityProvider{})
}

type antigravityProvider struct{}

func (antigravityProvider) Type() SourceType { return SourceTypeAntigravityGemini }

func (antigravityProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]ChatSource, error) {
	return discoverAntigravityGeminiSessions(env.HomeDir, projectPath, env.GeminiHomeDir)
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

func (antigravityProvider) ReadMessages(source ChatSource) ([]readers.ChatMessage, error) {
	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".pb", ".pbtxt":
		messages, err := readers.ReadProtobuf(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read protobuf chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".jsonl", ".json":
		messages, err := readers.ReadAntigravityGemini(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read antigravity chat source %q: %w", source.Path, err)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported antigravity chat source file %q (supported: .pb, .pbtxt, .jsonl)", source.Path)
	}
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
	scanner.Buffer(make([]byte, probeInitialBufferSize), probeMaxBufferSize)

	linesRead := 0
	for scanner.Scan() {
		linesRead++
		if linesRead > probeLineLimit {
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
		if path := recursiveExtract(record, claudeCWDEvidenceKeys, 8); path != "" {
			return path, true
		}
	}

	return "", false
}

func probeAntigravityTextEvidence(content string) (string, bool) {
	linesRead := 0
	for _, rawLine := range strings.Split(content, "\n") {
		linesRead++
		if linesRead > probeLineLimit {
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
