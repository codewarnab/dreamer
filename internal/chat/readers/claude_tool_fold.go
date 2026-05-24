package readers

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxToolOutputChars caps folded tool output to keep transcripts compact.
	maxToolOutputChars = 500
	// maxToolInputChars caps serialized tool input for non-essential tools.
	maxToolInputChars = 200
	// DropToolDetails controls whether folded tool messages include input/output.
	// When true, folded messages emit only "[ToolName]" with no command or result.
	// When false, folded messages emit "[ToolName] input\n→ output".
	DropToolDetails = true
)

type toolUseBlock struct {
	id        string
	name      string
	input     map[string]any
	timestamp time.Time
}

type toolResultBlock struct {
	toolUseID string
	content   string
	isError   bool
}

type pendingToolCall struct {
	name      string
	input     string
	timestamp time.Time
}

// ReadClaudeJSONLWithToolFolding reads a Claude session JSONL and folds
// tool_use + tool_result pairs into single compact messages. Tool interactions
// are preserved as "[ToolName] input → output" instead of being dropped.
func ReadClaudeJSONLWithToolFolding(filePath string) ([]ChatMessage, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open jsonl file %q: %w", filePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)

	var messages []ChatMessage
	pending := make(map[string]pendingToolCall)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			continue
		}

		msgs := processClaudeRecord(record, pending)
		messages = append(messages, msgs...)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan jsonl file %q: %w", filePath, err)
	}

	// Emit any orphaned tool calls that never received a result.
	for id, call := range pending {
		messages = append(messages, ChatMessage{
			Role:      "assistant",
			Content:   formatToolCall(call.name, call.input, ""),
			Timestamp: call.timestamp,
			ToolName:  call.name,
		})
		delete(pending, id)
	}

	return messages, nil
}

// processClaudeRecord handles a single JSONL record, extracting text messages
// and tool_use/tool_result blocks. Tool calls are stored in pending for later
// folding with their results.
func processClaudeRecord(record map[string]any, pending map[string]pendingToolCall) []ChatMessage {
	// Extract the message payload.
	msgPayload := extractMessagePayload(record)
	if msgPayload == nil {
		return nil
	}

	role := normalizeRole(rawRole(msgPayload["role"]))
	if role == "" {
		return nil
	}

	timestamp := timestampFromRecord(record)
	if timestamp.IsZero() {
		timestamp = timestampFromRecord(msgPayload)
	}

	content, ok := msgPayload["content"]
	if !ok {
		return nil
	}

	// Try to parse as a structured content array with tool blocks.
	blocks := parseContentArray(content)
	if blocks == nil {
		// Fall back to plain text extraction.
		text := textFromValue(content, 0)
		if text == "" {
			return nil
		}
		return []ChatMessage{{Role: role, Content: text, Timestamp: timestamp}}
	}

	return processContentBlocks(role, timestamp, blocks, pending)
}

// extractMessagePayload finds the message object within a Claude JSONL record.
func extractMessagePayload(record map[string]any) map[string]any {
	// Direct "message" key (standard Claude format).
	if messagePayload, ok := record["message"].(map[string]any); ok {
		return messagePayload
	}
	// Record itself might be the message.
	if _, ok := record["role"].(string); ok {
		return record
	}
	return nil
}

type contentBlock struct {
	blockType string
	text      string           // for type="text"
	toolUse   *toolUseBlock    // for type="tool_use"
	toolRes   *toolResultBlock // for type="tool_result"
}

func parseTextBlock(block map[string]any) (contentBlock, bool) {
	text, _ := block["text"].(string)
	if strings.TrimSpace(text) != "" {
		return contentBlock{blockType: "text", text: strings.TrimSpace(text)}, true
	}
	return contentBlock{}, false
}

func parseToolUseBlock(block map[string]any) (contentBlock, bool) {
	id, _ := block["id"].(string)
	name, _ := block["name"].(string)
	input, _ := block["input"].(map[string]any)
	if id != "" && name != "" {
		return contentBlock{
			blockType: "tool_use",
			toolUse:   &toolUseBlock{id: id, name: name, input: input},
		}, true
	}
	return contentBlock{}, false
}

func parseToolResultBlock(block map[string]any) (contentBlock, bool) {
	toolUseID, _ := block["tool_use_id"].(string)
	isError, _ := block["is_error"].(bool)
	content := textFromValue(block["content"], 0)
	if toolUseID != "" {
		return contentBlock{
			blockType: "tool_result",
			toolRes:   &toolResultBlock{toolUseID: toolUseID, content: content, isError: isError},
		}, true
	}
	return contentBlock{}, false
}

func parseUnknownBlock(block map[string]any) (contentBlock, bool) {
	text := textFromValue(block, 0)
	if text != "" {
		return contentBlock{blockType: "text", text: text}, true
	}
	return contentBlock{}, false
}

// parseContentArray attempts to parse a content value as a structured array
// of blocks. Returns nil if the content is a plain string or doesn't contain
// recognizable block types.
func parseContentArray(content any) []contentBlock {
	arr, ok := content.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	var blocks []contentBlock
	hasStructured := false

	for _, item := range arr {
		block, ok := item.(map[string]any)
		if !ok {
			// Might be a string element in the array.
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				blocks = append(blocks, contentBlock{blockType: "text", text: strings.TrimSpace(s)})
			}
			continue
		}

		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if b, ok := parseTextBlock(block); ok {
				blocks = append(blocks, b)
			}
		case "tool_use":
			hasStructured = true
			if b, ok := parseToolUseBlock(block); ok {
				blocks = append(blocks, b)
			}
		case "tool_result":
			hasStructured = true
			if b, ok := parseToolResultBlock(block); ok {
				blocks = append(blocks, b)
			}
		default:
			if b, ok := parseUnknownBlock(block); ok {
				blocks = append(blocks, b)
			}
		}
	}

	if !hasStructured && len(blocks) == 0 {
		return nil
	}
	return blocks
}

// processContentBlocks processes parsed content blocks, emitting text messages
// immediately and folding tool_use + tool_result pairs.
func processContentBlocks(role string, ts time.Time, blocks []contentBlock, pending map[string]pendingToolCall) []ChatMessage {
	var messages []ChatMessage

	for _, block := range blocks {
		switch block.blockType {
		case "text":
			messages = append(messages, ChatMessage{Role: role, Content: block.text, Timestamp: ts})

		case "tool_use":
			var input string
			if !DropToolDetails {
				input = serializeToolInput(block.toolUse.name, block.toolUse.input)
			}
			pending[block.toolUse.id] = pendingToolCall{
				name:      block.toolUse.name,
				input:     input,
				timestamp: ts,
			}

		case "tool_result":
			if call, ok := pending[block.toolRes.toolUseID]; ok {
				delete(pending, block.toolRes.toolUseID)
				var output string
				if !DropToolDetails {
					output = truncateToolOutput(block.toolRes.content)
				}
				messages = append(messages, ChatMessage{
					Role:      "assistant",
					Content:   formatToolCall(call.name, call.input, output),
					Timestamp: call.timestamp,
					ToolName:  call.name,
				})
			} else if !DropToolDetails && block.toolRes.content != "" {
				// Orphaned result — no matching tool_use found.
				output := truncateToolOutput(block.toolRes.content)
				messages = append(messages, ChatMessage{
					Role:      "assistant",
					Content:   fmt.Sprintf("[tool_result] %s", output),
					Timestamp: ts,
				})
			}
		}
	}

	return messages
}

// serializeToolInput extracts a compact string representation of tool input.
func serializeToolInput(toolName string, input map[string]any) string {
	if input == nil {
		return ""
	}

	// For common tools, extract the most relevant field.
	switch toolName {
	case "Bash", "bash":
		if cmd, ok := input["command"].(string); ok {
			return cmd
		}
	case "Read", "read":
		if filePath, ok := input["file_path"].(string); ok {
			return filePath
		}
		if filePath, ok := input["path"].(string); ok {
			return filePath
		}
	case "Edit", "edit", "Write", "write":
		if filePath, ok := input["file_path"].(string); ok {
			return filePath
		}
		if filePath, ok := input["path"].(string); ok {
			return filePath
		}
	case "Grep", "grep":
		if pattern, ok := input["pattern"].(string); ok {
			return pattern
		}
	case "Glob", "glob":
		if pattern, ok := input["pattern"].(string); ok {
			return pattern
		}
	}

	// Fallback: JSON-serialize, capped.
	raw, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	serialized := string(raw)
	if len(serialized) > maxToolInputChars {
		return serialized[:maxToolInputChars] + "..."
	}
	return serialized
}

// formatToolCall produces the folded "[ToolName] input → output" format.
func formatToolCall(name, input, output string) string {
	var sb strings.Builder
	sb.WriteString("[")
	sb.WriteString(name)
	sb.WriteString("]")
	if input != "" {
		sb.WriteString(" ")
		sb.WriteString(input)
	}
	if output != "" {
		sb.WriteString("\n→ ")
		sb.WriteString(output)
	}
	return sb.String()
}

// truncateToolOutput caps output at maxToolOutputChars runes.
// Slices at a rune boundary to avoid cutting multi-byte UTF-8 characters,
// and uses strconv.Itoa to avoid fmt.Sprintf's reflection overhead.
func truncateToolOutput(output string) string {
	output = strings.TrimSpace(output)
	runeCount := 0
	for i := range output {
		if runeCount == maxToolOutputChars {
			truncated := utf8.RuneCountInString(output[i:])
			return output[:i] + "… (" + strconv.Itoa(truncated) + " chars truncated)"
		}
		runeCount++
	}
	return output
}
