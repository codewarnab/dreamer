package readers

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxProtobufReaderDepth = 8
	maxProtobufCandidates  = 4096
	minCandidateLength     = 8
)

func ReadProtobuf(filePath string) ([]ChatMessage, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read protobuf file %q: %w", filePath, err)
	}

	messages, err := parseJSONRecordsFromBytes(data)
	if err != nil {
		return nil, fmt.Errorf("scan protobuf file %q: %w", filePath, err)
	}
	if len(messages) > 0 {
		return messages, nil
	}

	candidates := extractProtobufCandidates(data)
	if len(candidates) == 0 {
		return nil, nil
	}

	return messagesFromProtobufCandidates(candidates), nil
}

func parseJSONRecordsFromBytes(data []byte) ([]ChatMessage, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, initialScannerBufferSize), maxScannerBufferSize)

	messages := make([]ChatMessage, 0)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		message, ok := parseJSONLRecord(line)
		if !ok {
			continue
		}
		messages = append(messages, message)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return messages, nil
}

func extractProtobufCandidates(data []byte) []string {
	candidates := make([]string, 0, 128)
	collectProtoStrings(data, 0, &candidates)
	if len(candidates) == 0 {
		candidates = append(candidates, extractPrintableRuns(data)...)
	}
	return normalizeCandidates(candidates)
}

func collectProtoStrings(payload []byte, depth int, out *[]string) {
	if depth > maxProtobufReaderDepth || len(payload) == 0 || len(*out) >= maxProtobufCandidates {
		return
	}

	offset := 0
	for offset < len(payload) && len(*out) < maxProtobufCandidates {
		tag, n := binary.Uvarint(payload[offset:])
		if n <= 0 {
			return
		}
		offset += n

		wireType := tag & 0x7
		switch wireType {
		case 0:
			_, size := binary.Uvarint(payload[offset:])
			if size <= 0 {
				return
			}
			offset += size
		case 1:
			if offset+8 > len(payload) {
				return
			}
			offset += 8
		case 2:
			lengthValue, size := binary.Uvarint(payload[offset:])
			if size <= 0 {
				return
			}
			offset += size

			length := int(lengthValue)
			if length < 0 || offset+length > len(payload) {
				return
			}

			field := payload[offset : offset+length]
			offset += length

			if text, ok := decodeCandidateString(field); ok {
				*out = append(*out, text)
			}

			if likelyEmbeddedProto(field) {
				collectProtoStrings(field, depth+1, out)
			}
		case 5:
			if offset+4 > len(payload) {
				return
			}
			offset += 4
		default:
			return
		}
	}
}

func likelyEmbeddedProto(field []byte) bool {
	if len(field) < 2 {
		return false
	}
	if _, ok := decodeCandidateString(field); ok {
		return false
	}
	return true
}

func extractPrintableRuns(data []byte) []string {
	runs := make([]string, 0, 64)
	builder := strings.Builder{}

	flush := func() {
		candidate := strings.TrimSpace(builder.String())
		if len(candidate) >= minCandidateLength {
			runs = append(runs, candidate)
		}
		builder.Reset()
	}

	for _, raw := range data {
		if raw == '\n' || raw == '\r' || raw == '\t' || (raw >= 32 && raw <= 126) {
			builder.WriteByte(raw)
			continue
		}

		if builder.Len() > 0 {
			flush()
		}
	}
	if builder.Len() > 0 {
		flush()
	}

	return runs
}

func decodeCandidateString(raw []byte) (string, bool) {
	if !utf8.Valid(raw) {
		return "", false
	}

	candidate := strings.TrimSpace(string(raw))
	if len(candidate) < minCandidateLength {
		return "", false
	}

	printableRunes := 0
	letterOrDigit := 0
	for _, r := range candidate {
		if unicode.IsPrint(r) || r == '\n' || r == '\r' || r == '\t' {
			printableRunes++
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letterOrDigit++
		}
	}
	if printableRunes == 0 || letterOrDigit == 0 {
		return "", false
	}
	if float64(printableRunes)/float64(len([]rune(candidate))) < 0.85 {
		return "", false
	}

	return candidate, true
}

func normalizeCandidates(candidates []string) []string {
	normalized := make([]string, 0, len(candidates))
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		cleaned := strings.TrimSpace(candidate)
		if cleaned == "" || len(cleaned) < minCandidateLength {
			continue
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func messagesFromProtobufCandidates(candidates []string) []ChatMessage {
	jsonMessages := make([]ChatMessage, 0, len(candidates))
	nonJSONCandidates := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if message, ok := messageFromJSONString(candidate); ok {
			jsonMessages = append(jsonMessages, message)
			continue
		}
		nonJSONCandidates = append(nonJSONCandidates, candidate)
	}
	if len(jsonMessages) > 0 {
		return dedupeMessages(jsonMessages)
	}

	messages := make([]ChatMessage, 0, len(nonJSONCandidates))
	nextRole := "user"

	for _, candidate := range nonJSONCandidates {
		role, content := inferRoleAndContent(candidate, nextRole)
		if content == "" {
			continue
		}
		messages = append(messages, ChatMessage{
			Role:    role,
			Content: content,
		})
		nextRole = oppositeRole(role)
	}

	return dedupeMessages(messages)
}

func messageFromJSONString(candidate string) (ChatMessage, bool) {
	trimmed := strings.TrimSpace(candidate)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return ChatMessage{}, false
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
		return ChatMessage{}, false
	}

	return messageFromMap(record, record)
}

func inferRoleAndContent(candidate string, fallbackRole string) (string, string) {
	normalized := strings.TrimSpace(candidate)
	lowered := strings.ToLower(normalized)

	userPrefixes := []string{"user:", "human:", "prompt:"}
	for _, prefix := range userPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			content := strings.TrimSpace(normalized[len(prefix):])
			return "user", content
		}
	}

	assistantPrefixes := []string{"assistant:", "model:", "gemini:", "ai:"}
	for _, prefix := range assistantPrefixes {
		if strings.HasPrefix(lowered, prefix) {
			content := strings.TrimSpace(normalized[len(prefix):])
			return "assistant", content
		}
	}

	if normalized == "" {
		return "", ""
	}
	if fallbackRole == "assistant" {
		return "assistant", normalized
	}
	return "user", normalized
}

func oppositeRole(role string) string {
	if role == "assistant" {
		return "user"
	}
	return "assistant"
}

func dedupeMessages(messages []ChatMessage) []ChatMessage {
	deduped := make([]ChatMessage, 0, len(messages))
	seen := map[string]struct{}{}
	for _, message := range messages {
		key := message.Role + "\x00" + strings.TrimSpace(message.Content)
		if key == "\x00" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, message)
	}
	return deduped
}
