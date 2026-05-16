package pipeline

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/logging"
)

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

func buildRedactedTranscript(sources []chat.ChatSource, redactor *analyzer.Redactor, logger *logging.Logger) (string, []chat.ChatSource, int, []string, int, error) {
	var b strings.Builder
	usedSources := make([]chat.ChatSource, 0, len(sources))
	warnings := []string{}
	messageCount := 0
	totalHits := 0
	for _, source := range sources {
		messages, err := readMessagesFromSource(source)
		if err != nil {
			logger.Warn("source read failed path=%q tool=%s error=%v", source.Path, source.Tool, err)
			warnings = append(warnings, fmt.Sprintf("Skipped %s (%v).", source.Path, err))
			continue
		}
		raw := len(messages)
		if source.Tool == chat.SourceTypeClaudeCodeSession && strings.ToLower(filepath.Ext(source.Path)) == ".jsonl" {
			messages = readers.SanitizeClaudeMessages(messages)
		}
		if len(messages) == 0 {
			logger.Info("source empty path=%q tool=%s raw=%d", source.Path, source.Tool, raw)
			continue
		}
		usedSources = append(usedSources, source)
		fmt.Fprintf(&b, "source: %s\n", source.Path)
		fmt.Fprintf(&b, "tool: %s\n\n", source.Tool)
		sourceMessages := 0
		sourceHits := 0
		for _, message := range messages {
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			redacted, result := redactor.Redact(text)
			totalHits += result.TotalHits()
			sourceHits += result.TotalHits()
			messageCount++
			sourceMessages++
			if !message.Timestamp.IsZero() {
				b.WriteString("[")
				b.WriteString(message.Timestamp.UTC().Format(time.RFC3339))
				b.WriteString("] ")
			}
			b.WriteString(message.Role)
			b.WriteString(": ")
			b.WriteString(redacted)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
		logger.Info("source read path=%q tool=%s raw=%d kept=%d redactions=%d",
			source.Path, source.Tool, raw, sourceMessages, sourceHits)
	}
	return b.String(), usedSources, messageCount, warnings, totalHits, nil
}
