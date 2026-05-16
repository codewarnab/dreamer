package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// claudeCWDEvidenceKeys is the canonical "where did this session run?" key set
// used by Claude Code, the Antigravity JSON evidence probe, and Gemini CLI's
// secondary scan.
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

// walkChatFiles enumerates files under root whose extension is in extensions
// and returns one ChatSource per match. Missing roots yield no sources.
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

// probeJSONLForCWD scans up to maxLines JSON lines from path and returns the
// first non-empty result produced by extract. Callers supply their own
// per-source extractor.
func probeJSONLForCWD(path string, maxLines int, extract func(record map[string]any) string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, probeInitialBufferSize), probeMaxBufferSize)

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

		if path := extract(record); path != "" {
			return path, true
		}
	}

	return "", false
}

// recursiveExtract walks value depth-first and returns the first string found
// under any key in evidenceKeys. Matched subtrees are passed through
// extractPathValue so common path wrappers like {path: "..."} are handled.
func recursiveExtract(value any, evidenceKeys map[string]struct{}, maxDepth int) string {
	return recursiveExtractWalker(value, evidenceKeys, maxDepth, 0)
}

func recursiveExtractWalker(value any, keys map[string]struct{}, maxDepth int, depth int) string {
	if depth > maxDepth || value == nil {
		return ""
	}

	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if _, ok := keys[normalizeDiscoveryKey(key)]; ok {
				if path := extractPathValue(nested, depth+1); path != "" {
					return path
				}
			}
		}
		for _, nested := range typed {
			if path := recursiveExtractWalker(nested, keys, maxDepth, depth+1); path != "" {
				return path
			}
		}
	case []any:
		for _, nested := range typed {
			if path := recursiveExtractWalker(nested, keys, maxDepth, depth+1); path != "" {
				return path
			}
		}
	}

	return ""
}

// extractPathValue unwraps a JSON value into a single path string, honouring
// the standard set of nested path wrappers chat sources tend to use.
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

// splitDiscoveryField parses a `key: value` line, trimming whitespace and
// surrounding quotes from the value. Used by the Antigravity text probe.
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

func normalizeDiscoveryKey(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	return normalized
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
