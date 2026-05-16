package pipeline

import (
	"fmt"
	"strings"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/chat"
	"dreamer/internal/chat/readers"
	"dreamer/internal/logging"
)

func readMessagesFromSource(source chat.ChatSource) ([]readers.ChatMessage, error) {
	provider, ok := chat.ProviderFor(source.Tool)
	if !ok {
		return nil, fmt.Errorf("unsupported chat source tool %q for %q", source.Tool, source.Path)
	}
	return provider.ReadMessages(source)
}

// buildProviderBlocks reads + redacts every source and groups results by tool.
// Returns one ProviderBlock per tool with messages in discovery order.
func buildProviderBlocks(sources []chat.ChatSource, redactor *analyzer.Redactor, logger *logging.Logger) ([]ProviderBlock, []chat.ChatSource, int, []string, int, error) {
	type accumulator struct {
		tool     string
		paths    []string
		messages []string
	}
	byTool := map[string]*accumulator{}
	toolOrder := []string{}
	usedSources := make([]chat.ChatSource, 0, len(sources))
	warnings := []string{}
	messageCount := 0
	totalHits := 0
	for _, source := range sources {
		messages, err := readMessagesFromSource(source)
		if err != nil {
			logger.Warn("source read failed", logging.Any("path", source.Path), logging.Any("tool", source.Tool), logging.Any("err", err))
			warnings = append(warnings, fmt.Sprintf("Skipped %s (%v).", source.Path, err))
			continue
		}
		raw := len(messages)
		if len(messages) == 0 {
			logger.Info("source empty", logging.Any("path", source.Path), logging.Any("tool", source.Tool), logging.Any("raw", raw))
			continue
		}
		usedSources = append(usedSources, source)
		toolKey := string(source.Tool)
		acc, ok := byTool[toolKey]
		if !ok {
			acc = &accumulator{tool: toolKey}
			byTool[toolKey] = acc
			toolOrder = append(toolOrder, toolKey)
		}
		acc.paths = append(acc.paths, source.Path)
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
			var line strings.Builder
			if !message.Timestamp.IsZero() {
				line.WriteString("[")
				line.WriteString(message.Timestamp.UTC().Format(time.RFC3339))
				line.WriteString("] ")
			}
			line.WriteString(message.Role)
			line.WriteString(": ")
			line.WriteString(redacted)
			line.WriteByte('\n')
			acc.messages = append(acc.messages, line.String())
		}
		logger.Info("source read", logging.Any("path", source.Path), logging.Any("tool", source.Tool), logging.Any("raw", raw), logging.Any("kept", sourceMessages), logging.Any("redactions", sourceHits))
	}

	blocks := make([]ProviderBlock, 0, len(toolOrder))
	for _, tool := range toolOrder {
		acc := byTool[tool]
		header := fmt.Sprintf("tool: %s\nsources: %s\n\n", tool, strings.Join(acc.paths, ", "))
		blocks = append(blocks, ProviderBlock{
			Tool:     tool,
			Sources:  acc.paths,
			Header:   header,
			Messages: acc.messages,
		})
	}
	return blocks, usedSources, messageCount, warnings, totalHits, nil
}
