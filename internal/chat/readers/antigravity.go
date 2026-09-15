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

	// minVectorPayloadCommas is the minimum number of commas required for
	// a JSON array to qualify as a likely vector embedding payload.
	minVectorPayloadCommas = 20
)

type antigravityDropRule struct {
	name               string
	normalizedTokens   []string
	normalizedPrefixes []string
}

var (
	userRequestPattern        = regexp.MustCompile(`(?is)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
	userRequestTagPattern     = regexp.MustCompile(`(?i)</?USER_REQUEST>`)
	additionalMetadataPattern = regexp.MustCompile(`(?is)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
	userSettingsPattern       = regexp.MustCompile(`(?is)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`)
	systemMessagePattern      = regexp.MustCompile(`(?is)<SYSTEM_MESSAGE>.*?</SYSTEM_MESSAGE>`)
)

var antigravityDropRules = []antigravityDropRule{
	{
		name: "tool-records",
		normalizedTokens: []string{
			"tool_call", "toolcalls", "tool call", "tool_result", "tool result",
			"function_call", "function response", "command output", "stdout:", "stderr:",
			"the command exited with code", "tool is running as a background task",
		},
		normalizedPrefixes: []string{"created at:"},
	},
	{
		name:             "runtime-debug-state",
		normalizedTokens: []string{"runtime state", "debug payload", "stack trace id", "trace metadata", "serialized internal", "internal state", "checkpoint", "<system_message>"},
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
	result, err := ReadAntigravityGeminiWithBudget(filePath, ReadBudget{})
	return result.Messages, err
}

func ReadAntigravityGeminiWithBudget(filePath string, budget ReadBudget) (ReadResult, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".jsonl":
		result, err := ReadJSONLWithOptionsResult(filePath, JSONLReadOptions{Budget: budget})
		if err != nil {
			return ReadResult{}, err
		}
		return ReadResult{
			Messages:  SanitizeAntigravityMessages(result.Messages),
			Truncated: result.Truncated,
			Bytes:     result.Bytes,
		}, nil
	case ".pbtxt":
		data, err := os.ReadFile(filePath)
		if err != nil {
			return ReadResult{}, fmt.Errorf("read antigravity text protobuf file %q: %w", filePath, err)
		}
		if messages := parseAntigravityTextProtoMessages(string(data)); len(messages) > 0 {
			return ReadResult{Messages: SanitizeAntigravityMessages(messages)}, nil
		}
		messages, err := readAntigravityProtobuf(filePath)
		return ReadResult{Messages: messages}, err
	case ".pb":
		messages, err := readAntigravityProtobuf(filePath)
		return ReadResult{Messages: messages}, err
	default:
		return ReadResult{}, fmt.Errorf("unsupported antigravity chat source file %q (supported: .jsonl, .pb, .pbtxt)", filePath)
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
	return sanitizeMessages(messages, normalizeAntigravityContent, func(_, normalizedLower string) bool {
		return shouldDropAntigravityMessage(normalizedLower)
	}, antigravityMaxMessagesPerSource)
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
	cleaned := additionalMetadataPattern.ReplaceAllString(content, "")
	cleaned = userSettingsPattern.ReplaceAllString(cleaned, "")
	cleaned = systemMessagePattern.ReplaceAllString(cleaned, "")

	if matches := userRequestPattern.FindAllStringSubmatch(cleaned, -1); len(matches) > 0 {
		var parts []string
		for _, match := range matches {
			if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
				parts = append(parts, strings.TrimSpace(match[1]))
			}
		}
		if len(parts) > 0 {
			cleaned = strings.Join(parts, "\n")
		} else {
			cleaned = userRequestTagPattern.ReplaceAllString(cleaned, "")
		}
	} else {
		cleaned = userRequestTagPattern.ReplaceAllString(cleaned, "")
	}

	normalized := ANSIEscapePattern.ReplaceAllString(cleaned, "")
	normalized = WhitespaceBurstRegex.ReplaceAllString(normalized, " ")
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return ""
	}

	// Truncate to antigravityMaxCharsPerMessage runes without allocating a
	// []rune slice. Ranging over a string yields rune boundaries at zero cost.
	runeCount := 0
	for i := range normalized {
		if runeCount == antigravityMaxCharsPerMessage {
			normalized = strings.TrimSpace(normalized[:i])
			break
		}
		runeCount++
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
	return commas >= minVectorPayloadCommas && !strings.Contains(content, " ")
}
