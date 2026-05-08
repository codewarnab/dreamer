package readers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	antigravityMaxCharsPerMessage   = 4000
	antigravityMaxMessagesPerSource = 250
)

var (
	antigravityANSIEscapePattern    = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	antigravityWhitespaceBurstRegex = regexp.MustCompile(`[\t\r\n ]+`)
)

type antigravityDropRule struct {
	name               string
	normalizedTokens   []string
	normalizedPrefixes []string
}

var antigravityDropRules = []antigravityDropRule{
	{
		name:             "tool-records",
		normalizedTokens: []string{"tool_call", "toolcalls", "tool call", "tool_result", "tool result", "function_call", "function response", "command output", "stdout:", "stderr:"},
	},
	{
		name:             "runtime-debug-state",
		normalizedTokens: []string{"runtime state", "debug payload", "stack trace id", "trace metadata", "serialized internal", "internal state", "checkpoint"},
	},
	{
		name:             "model-config-metadata",
		normalizedTokens: []string{"model:", "model config", "temperature:", "top_p", "candidate count", "safety settings", "embedding", "vector embedding"},
	},
	{
		name:             "workspace-evidence",
		normalizedTokens: []string{"cwd:", "workspace:", "workspace path:", "project path:", "root path:", "repository root:"},
	},
	{
		name:               "task-inbox-metadata",
		normalizedTokens:   []string{"inbox item", "task id", "annotation id", "conversation id", "message id"},
		normalizedPrefixes: []string{"title:", "path:"},
	},
}

// ReadAntigravityGemini reads Antigravity/Gemini chat files and returns only
// explicit user/assistant conversation turns.
//
// Antigravity stores protobuf and text records with project metadata, runtime
// state, tool activity, and chat content close together. This reader keeps the
// source-specific cleanup here so callers can continue using the simple
// ChatMessage contract without knowing the storage details.
func ReadAntigravityGemini(filePath string) ([]ChatMessage, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".jsonl":
		messages, err := ReadJSONL(filePath)
		if err != nil {
			return nil, err
		}
		return SanitizeAntigravityMessages(messages), nil
	case ".pbtxt":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read antigravity text protobuf file %q: %w", filePath, err)
		}
		if messages := parseAntigravityTextProtoMessages(string(data)); len(messages) > 0 {
			return SanitizeAntigravityMessages(messages), nil
		}
		return readAntigravityProtobuf(filePath)
	case ".pb":
		return readAntigravityProtobuf(filePath)
	default:
		return nil, fmt.Errorf("unsupported antigravity chat source file %q (supported: .jsonl, .pb, .pbtxt)", filePath)
	}
}

func readAntigravityProtobuf(filePath string) ([]ChatMessage, error) {
	messages, err := ReadProtobuf(filePath)
	if err != nil {
		return nil, err
	}
	return SanitizeAntigravityMessages(messages), nil
}

// SanitizeAntigravityMessages normalizes Antigravity chat turns and removes
// source noise that is not useful for analyzer input.
func SanitizeAntigravityMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}

	sanitized := make([]ChatMessage, 0, min(len(messages), antigravityMaxMessagesPerSource))
	previousKey := ""

	for _, message := range messages {
		normalizedContent := normalizeAntigravityContent(message.Content)
		if normalizedContent == "" {
			continue
		}

		normalizedLower := strings.ToLower(normalizedContent)
		if shouldDropAntigravityMessage(normalizedLower) {
			continue
		}

		dedupeKey := message.Role + "\x00" + normalizedContent
		if dedupeKey == previousKey {
			continue
		}

		message.Content = normalizedContent
		sanitized = append(sanitized, message)
		previousKey = dedupeKey

		if antigravityMaxMessagesPerSource > 0 && len(sanitized) >= antigravityMaxMessagesPerSource {
			break
		}
	}

	return sanitized
}

func parseAntigravityTextProtoMessages(content string) []ChatMessage {
	messages := make([]ChatMessage, 0)
	pendingRole := ""

	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		key, value, ok := splitTextProtoField(line)
		if !ok {
			if message, ok := messageFromRolePrefixedString(unquoteTextProtoValue(line)); ok {
				messages = append(messages, message)
				pendingRole = ""
			}
			continue
		}

		if role := normalizeRole(value); isAntigravityRoleKey(key) && role != "" {
			pendingRole = role
			continue
		}

		if pendingRole == "" || !isAntigravityTextKey(key) {
			continue
		}

		text := unquoteTextProtoValue(value)
		if text == "" {
			continue
		}
		messages = append(messages, ChatMessage{
			Role:    pendingRole,
			Content: text,
		})
		pendingRole = ""
	}

	return dedupeMessages(messages)
}

func splitTextProtoField(line string) (string, string, bool) {
	index := strings.Index(line, ":")
	if index <= 0 {
		return "", "", false
	}
	key := strings.ToLower(strings.TrimSpace(line[:index]))
	value := unquoteTextProtoValue(line[index+1:])
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func unquoteTextProtoValue(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, `"`)
	return strings.TrimSpace(trimmed)
}

func isAntigravityRoleKey(key string) bool {
	switch key {
	case "role", "author", "sender":
		return true
	default:
		return false
	}
}

func isAntigravityTextKey(key string) bool {
	switch key {
	case "text", "content", "message", "body", "prompt", "response":
		return true
	default:
		return false
	}
}

func normalizeAntigravityContent(content string) string {
	normalized := antigravityANSIEscapePattern.ReplaceAllString(content, "")
	normalized = antigravityWhitespaceBurstRegex.ReplaceAllString(normalized, " ")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}

	runes := []rune(normalized)
	if len(runes) > antigravityMaxCharsPerMessage {
		normalized = strings.TrimSpace(string(runes[:antigravityMaxCharsPerMessage]))
	}

	return normalized
}

func shouldDropAntigravityMessage(normalizedLower string) bool {
	if looksLikeVectorPayload(normalizedLower) {
		return true
	}
	for _, rule := range antigravityDropRules {
		if containsAny(normalizedLower, rule.normalizedTokens...) {
			return true
		}
		if containsAnyPrefix(normalizedLower, rule.normalizedPrefixes...) {
			return true
		}
	}
	return false
}

func looksLikeVectorPayload(content string) bool {
	if !strings.HasPrefix(content, "[") || !strings.HasSuffix(content, "]") {
		return false
	}
	commas := strings.Count(content, ",")
	return commas >= 20 && !strings.Contains(content, " ")
}
