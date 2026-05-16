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
