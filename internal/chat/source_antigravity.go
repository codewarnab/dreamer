package chat

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat/readers"
)

func init() {
	registerProvider(antigravityProvider{fileBackedProvider{sourceType: SourceTypeAntigravityGemini}})
}

type antigravityProvider struct {
	fileBackedProvider
}

func (antigravityProvider) Discover(env DiscoveryEnvironment, projectPath string) ([]Source, error) {
	return discoverAntigravityGeminiSessions(env.HomeDir, projectPath, env.GeminiHomeDir, env.GeminiCLIHomeDir)
}

func discoverAntigravityGeminiSessions(homeDir string, projectPath string, geminiHomeDir string, cliHomeDirs ...string) ([]Source, error) {
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

	discovered := make([]Source, 0)
	seen := map[string]struct{}{}
	for _, root := range roots {
		for _, conversationsDir := range []string{"conversations", "inbox"} {
			sources, err := walkChatFiles(filepath.Join(root.path, conversationsDir), SourceTypeAntigravityGemini, map[string]struct{}{
				".pb":    {},
				".pbtxt": {},
				".jsonl": {},
			}, skipDreamerMarkedFiles)
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

	var cliHome string
	if len(cliHomeDirs) > 0 && strings.TrimSpace(cliHomeDirs[0]) != "" {
		cliHome = strings.TrimSpace(cliHomeDirs[0])
	} else if envVal := strings.TrimSpace(os.Getenv("GEMINI_CLI_HOME")); envVal != "" {
		cliHome = envVal
	} else if trimmedGeminiHome != "" && hasDir(filepath.Join(trimmedGeminiHome, "antigravity-cli")) {
		cliHome = filepath.Join(trimmedGeminiHome, "antigravity-cli")
	} else if strings.TrimSpace(homeDir) != "" {
		cliHome = filepath.Join(strings.TrimSpace(homeDir), ".gemini", "antigravity-cli")
	}

	if cliHome != "" {
		if info, err := os.Stat(cliHome); err == nil && info.IsDir() {
			cliSources, err := discoverAntigravityCLISources(cliHome, normalizedProjectPath, seen)
			if err != nil {
				return nil, err
			}
			discovered = append(discovered, cliSources...)
		}
	}

	return discovered, nil
}

func hasDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func discoverAntigravityCLISources(cliHome string, normalizedProjectPath string, seen map[string]struct{}) ([]Source, error) {
	discovered := make([]Source, 0)

	dbPath := filepath.Join(cliHome, "conversation_summaries.db")
	if _, err := os.Stat(dbPath); err == nil {
		dbSources, err := discoverAntigravitySourcesFromDB(dbPath, cliHome, normalizedProjectPath, seen)
		if err == nil {
			discovered = append(discovered, dbSources...)
		}
	}

	// Fallback filesystem discovery: scan brain/*/transcript.jsonl
	brainDir := filepath.Join(cliHome, "brain")
	matches, _ := filepath.Glob(filepath.Join(brainDir, "*", ".system_generated", "logs", "transcript.jsonl"))
	for _, transcriptPath := range matches {
		if _, ok := seen[transcriptPath]; ok {
			continue
		}
		if skipDreamerMarkedFiles(transcriptPath) {
			continue
		}
		if !antigravitySourceBelongsToProject(transcriptPath, normalizedProjectPath) {
			continue
		}
		info, err := os.Stat(transcriptPath)
		if err != nil {
			continue
		}
		seen[transcriptPath] = struct{}{}
		discovered = append(discovered, Source{
			Path:         transcriptPath,
			Tool:         SourceTypeAntigravityGemini,
			ModifiedTime: info.ModTime().UTC(),
		})
	}

	return discovered, nil
}

func discoverAntigravitySourcesFromDB(dbPath string, cliHome string, normalizedProjectPath string, seen map[string]struct{}) ([]Source, error) {
	db, err := sql.Open(readers.DefaultSQLiteDriverName, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open conversation_summaries.db %q: %w", dbPath, err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT conversation_id, workspace_uris, last_modified_time FROM conversation_summaries")
	if err != nil {
		return nil, fmt.Errorf("query conversation_summaries in %q: %w", dbPath, err)
	}
	defer rows.Close()

	sources := make([]Source, 0)
	for rows.Next() {
		var conversationID, urisJSON string
		var lastModifiedRaw any
		if err := rows.Scan(&conversationID, &urisJSON, &lastModifiedRaw); err != nil {
			continue
		}

		conversationID = strings.TrimSpace(conversationID)
		if conversationID == "" {
			continue
		}

		transcriptPath := filepath.Join(cliHome, "brain", conversationID, ".system_generated", "logs", "transcript.jsonl")
		if _, ok := seen[transcriptPath]; ok {
			continue
		}
		if skipDreamerMarkedFiles(transcriptPath) {
			continue
		}

		var uris []string
		if err := json.Unmarshal([]byte(urisJSON), &uris); err != nil {
			continue
		}

		belongsToProject := false
		for _, uri := range uris {
			decoded, ok := decodeFileURI(uri)
			if !ok {
				continue
			}
			normalizedURI, ok := normalizeDiscoveryEvidencePath(decoded)
			if !ok {
				continue
			}

			if pathWithinNormalizedRoot(normalizedURI, normalizedProjectPath) {
				belongsToProject = true
				break
			}

			if pathWithinNormalizedRoot(normalizedProjectPath, normalizedURI) {
				if antigravitySourceBelongsToProject(transcriptPath, normalizedProjectPath) {
					belongsToProject = true
					break
				}
			}
		}

		if !belongsToProject {
			continue
		}

		info, err := os.Stat(transcriptPath)
		if err != nil {
			continue
		}

		modTime, ok := readers.ParseTimestamp(lastModifiedRaw)
		if !ok {
			modTime = info.ModTime().UTC()
		}

		seen[transcriptPath] = struct{}{}
		sources = append(sources, Source{
			Path:         transcriptPath,
			Tool:         SourceTypeAntigravityGemini,
			ModifiedTime: modTime,
		})
	}

	if err := rows.Err(); err != nil {
		return sources, fmt.Errorf("iterate conversation_summaries rows: %w", err)
	}

	return sources, nil
}

func (antigravityProvider) ReadMessages(source Source) ([]readers.ChatMessage, error) {
	result, err := antigravityProvider{}.ReadMessagesWithOptions(source, ReadOptions{})
	return result.Messages, err
}

func (antigravityProvider) ReadMessagesWithOptions(source Source, options ReadOptions) (ReadResult, error) {
	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".pb", ".pbtxt":
		messages, err := readers.ReadProtobuf(source.Path)
		if err != nil {
			return ReadResult{}, fmt.Errorf("read protobuf chat source %q: %w", source.Path, err)
		}
		return ReadResult{Messages: messages}, nil
	case ".jsonl", ".json":
		result, err := readers.ReadAntigravityGeminiWithBudget(source.Path, options.Budget)
		if err != nil {
			return ReadResult{}, fmt.Errorf("read antigravity chat source %q: %w", source.Path, err)
		}
		return ReadResult{Messages: result.Messages, Truncated: result.Truncated, Bytes: result.Bytes}, nil
	default:
		return ReadResult{}, fmt.Errorf("unsupported antigravity chat source file %q (supported: .pb, .pbtxt, .jsonl)", source.Path)
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
	if strings.HasSuffix(strings.ToLower(sourcePath), ".jsonl") {
		return probeJSONLForCWD(sourcePath, probeLineLimit, func(record map[string]any) string {
			raw := recursiveExtract(record, claudeCWDEvidenceKeys, probeMaxDepth)
			return strings.Trim(strings.TrimSpace(raw), "\"'`")
		})
	}

	payload, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", false
	}

	if path, ok := probeAntigravityJSONEvidence(payload); ok {
		return strings.Trim(strings.TrimSpace(path), "\"'`"), true
	}
	path, ok := probeAntigravityTextEvidence(string(payload))
	return strings.Trim(strings.TrimSpace(path), "\"'`"), ok
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
		if path := recursiveExtract(record, claudeCWDEvidenceKeys, probeMaxDepth); path != "" {
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
