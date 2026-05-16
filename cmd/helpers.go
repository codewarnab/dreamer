package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/config"
)

const defaultConfigFileName = "config.yaml"

func resolveConfigPath(configPath string) (string, error) {
	if strings.TrimSpace(configPath) == "" {
		path, err := config.GlobalConfigPath()
		if err != nil {
			return "", fmt.Errorf("resolve global config path: %w", err)
		}
		return path, nil
	}

	expandedPath, err := expandHomePath(configPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expandedPath) {
		absolutePath, err := filepath.Abs(expandedPath)
		if err != nil {
			return "", fmt.Errorf("resolve absolute config path %q: %w", configPath, err)
		}
		return absolutePath, nil
	}

	return filepath.Clean(expandedPath), nil
}

func expandHomePath(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home: %w", err)
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, path[2:]), nil
	}
	return path, nil
}

func readMessagesFromSource(source chat.ChatSource) ([]readers.ChatMessage, error) {
	if source.Tool == chat.SourceTypeAntigravityGemini {
		messages, err := readers.ReadAntigravityGemini(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read antigravity chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeGeminiCLISession {
		messages, err := readers.ReadGeminiCLI(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read gemini cli chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeOpenCodeSession {
		dbPath, sessionID := chat.SplitSQLiteSourcePath(source.Path)
		messages, err := readers.ReadOpenCodeMessages(dbPath, sessionID)
		if err != nil {
			return nil, fmt.Errorf("read opencode chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	if source.Tool == chat.SourceTypeKiroCLISession {
		dbPath, conversationID := chat.SplitSQLiteSourcePath(source.Path)
		messages, err := readers.ReadKiroConversation(dbPath, conversationID)
		if err != nil {
			return nil, fmt.Errorf("read kiro cli chat source %q: %w", source.Path, err)
		}
		return messages, nil
	}

	switch strings.ToLower(filepath.Ext(source.Path)) {
	case ".jsonl":
		if source.Tool == chat.SourceTypeVSCodeChatSession {
			messages, err := readers.ReadVSCodeChat(source.Path)
			if err != nil {
				return nil, fmt.Errorf("read vscode chat source %q: %w", source.Path, err)
			}
			return messages, nil
		}
		messages, err := readers.ReadJSONLWithOptions(source.Path, readers.JSONLReadOptions{
			SanitizeClaude:         source.Tool == chat.SourceTypeClaudeCodeSession,
			SanitizeCodex:          source.Tool == chat.SourceTypeCodexSessionJSONL,
			SanitizeCopilotSession: source.Tool == chat.SourceTypeCopilotSessionJSONL,
		})
		if err != nil {
			return nil, fmt.Errorf("read jsonl chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".json":
		if source.Tool != chat.SourceTypeVSCodeChatSession {
			return nil, fmt.Errorf("unsupported chat source file %q (supported: .json only for vscode chat sources)", source.Path)
		}
		messages, err := readers.ReadVSCodeChat(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read vscode chat source %q: %w", source.Path, err)
		}
		return messages, nil
	case ".pb", ".pbtxt":
		messages, err := readers.ReadProtobuf(source.Path)
		if err != nil {
			return nil, fmt.Errorf("read protobuf chat source %q: %w", source.Path, err)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("unsupported chat source file %q (supported: .jsonl, .json for vscode, .pb, .pbtxt)", source.Path)
	}
}
