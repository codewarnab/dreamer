package readers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadVSCodeChat reads VS Code chat session files and returns only the user and
// assistant text that is useful for project analysis. VS Code stores many UI,
// workspace, tool, and model details alongside the conversation; those details
// are intentionally ignored here so callers can keep using the simple reader
// contract.
func ReadVSCodeChat(filePath string) ([]ChatMessage, error) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".json":
		return readVSCodeChatJSON(filePath)
	case ".jsonl":
		return readVSCodeChatJSONL(filePath)
	default:
		return nil, fmt.Errorf("unsupported vscode chat file %q", filePath)
	}
}

func readVSCodeChatJSON(filePath string) ([]ChatMessage, error) {
	payload, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read vscode chat file %q: %w", filePath, err)
	}

	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, fmt.Errorf("parse vscode chat file %q: %w", filePath, err)
	}

	return vscodeMessagesFromDocument(document), nil
}

func readVSCodeChatJSONL(filePath string) ([]ChatMessage, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open vscode chat jsonl file %q: %w", filePath, err)
	}
	defer file.Close()

	document := map[string]any{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}
		applyVSCodeChatJSONLRecord(document, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan vscode chat jsonl file %q: %w", filePath, err)
	}

	return vscodeMessagesFromDocument(document), nil
}

func applyVSCodeChatJSONLRecord(document map[string]any, record map[string]any) {
	if snapshot, ok := record["v"].(map[string]any); ok {
		if requests, ok := snapshot["requests"].([]any); ok {
			document["requests"] = requests
			return
		}
	}

	if requests, ok := record["requests"].([]any); ok {
		document["requests"] = requests
		return
	}

	applyVSCodeResponseDelta(document, record)
}

func applyVSCodeResponseDelta(document map[string]any, record map[string]any) {
	requestIndex, ok := vscodeDeltaRequestIndex(record)
	if !ok {
		return
	}

	requests, ok := document["requests"].([]any)
	if !ok || requestIndex < 0 || requestIndex >= len(requests) {
		return
	}

	request, ok := requests[requestIndex].(map[string]any)
	if !ok {
		return
	}

	if value, ok := record["v"]; ok {
		request["response"] = value
		return
	}
	if value, ok := record["value"]; ok {
		request["response"] = value
	}
}

func vscodeDeltaRequestIndex(record map[string]any) (int, bool) {
	if pathValues, ok := record["p"].([]any); ok {
		for index := 0; index+2 < len(pathValues); index++ {
			if pathValues[index] == "requests" && pathValues[index+2] == "response" {
				return intFromValue(pathValues[index+1])
			}
		}
	}

	path, ok := record["path"].(string)
	if !ok {
		return 0, false
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index := 0; index+2 < len(parts); index++ {
		if parts[index] == "requests" && parts[index+2] == "response" {
			parsed, err := strconv.Atoi(parts[index+1])
			return parsed, err == nil
		}
	}

	return 0, false
}

func intFromValue(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case string:
		parsed, err := strconv.Atoi(typed)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func vscodeMessagesFromDocument(document map[string]any) []ChatMessage {
	requests, ok := document["requests"].([]any)
	if !ok {
		return nil
	}

	messages := make([]ChatMessage, 0, len(requests)*2)
	for _, requestValue := range requests {
		request, ok := requestValue.(map[string]any)
		if !ok {
			continue
		}

		userText := vscodeRequestUserText(request)
		if userText != "" {
			messages = append(messages, ChatMessage{Role: "user", Content: userText})
		}

		responseText := vscodeResponseText(request["response"])
		if responseText != "" {
			messages = append(messages, ChatMessage{Role: "assistant", Content: responseText})
		}
	}

	return messages
}

func vscodeRequestUserText(request map[string]any) string {
	message, ok := request["message"].(map[string]any)
	if !ok {
		return ""
	}
	return normalizeVSCodeChatText(textFromValue(message["text"], 0))
}

func vscodeResponseText(value any) string {
	parts := make([]string, 0)
	collectVSCodeResponseText(value, &parts, 0)
	return normalizeVSCodeChatText(strings.Join(parts, "\n"))
}

func collectVSCodeResponseText(value any, parts *[]string, depth int) {
	const maxDepth = 8

	if depth > maxDepth || value == nil {
		return
	}

	switch typed := value.(type) {
	case string:
		text := normalizeVSCodeChatText(typed)
		if text != "" && !isNoisyVSCodeChatContent(text) {
			*parts = append(*parts, text)
		}
	case []any:
		for _, item := range typed {
			collectVSCodeResponseText(item, parts, depth+1)
		}
	case map[string]any:
		if shouldSkipVSCodeResponseMap(typed) {
			return
		}
		for _, key := range []string{"markdown", "text", "value", "content"} {
			if text := normalizeVSCodeChatText(textFromValue(typed[key], 0)); text != "" {
				if isNoisyVSCodeChatContent(text) {
					return
				}
				*parts = append(*parts, text)
				return
			}
		}
		for key, nested := range typed {
			if isNoisyVSCodeChatKey(key) {
				continue
			}
			collectVSCodeResponseText(nested, parts, depth+1)
		}
	}
}

func isNoisyVSCodeChatContent(content string) bool {
	normalized := strings.ToLower(strings.TrimSpace(content))
	return strings.HasPrefix(normalized, "instructions:") ||
		strings.HasPrefix(normalized, "skills:") ||
		strings.HasPrefix(normalized, "agents:") ||
		strings.Contains(normalized, "mcp server") ||
		strings.Contains(normalized, "tool call id")
}

func shouldSkipVSCodeResponseMap(record map[string]any) bool {
	for _, key := range []string{"kind", "type"} {
		value, ok := record[key].(string)
		if !ok {
			continue
		}
		if isNoisyVSCodeChatToken(value) {
			return true
		}
	}
	return false
}

func isNoisyVSCodeChatKey(key string) bool {
	return isNoisyVSCodeChatToken(key)
}

func isNoisyVSCodeChatToken(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")

	switch normalized {
	case "variabledata", "inputstate", "model", "editor", "workspace", "metadata",
		"instructions", "skills", "agents", "thinking", "toolinvocationserialized",
		"toolcallid", "toolcallids", "tooluseid", "mcp", "server", "tool":
		return true
	default:
		return false
	}
}

func normalizeVSCodeChatText(content string) string {
	normalized := strings.TrimSpace(content)
	if normalized == "" {
		return ""
	}
	normalized = copilotWhitespaceBurstRegex.ReplaceAllString(normalized, " ")
	return strings.TrimSpace(normalized)
}
