package acpcore

import (
	"encoding/json"
	"fmt"
	"os"

	"dreamer/internal/analyzer"
)

// handleSessionUpdate routes session/update notifications to the per-session
// text accumulator. Spec: params = {sessionId, update: {sessionUpdate, content?}}.
// We only collect agent_message_chunk text; tool_call / agent_thought_chunk
// are intentionally ignored for analyzer use.
func (t *transport) handleSessionUpdate(raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	if params.SessionID == "" || params.Update.SessionUpdate != "agent_message_chunk" {
		return
	}
	if params.Update.Content.Type != "text" {
		return
	}
	streamRaw, ok := t.streams.Load(params.SessionID)
	if !ok {
		return
	}
	if stream, ok := streamRaw.(*sessionStream); ok {
		stream.append(params.Update.Content.Text)
	}
}

func (t *transport) handlePermissionRequest(envelope rpcEnvelope) {
	t.mu.Lock()
	handler := t.permHandler
	t.mu.Unlock()

	approved, reason := decidePermission(handler, envelope.Params)

	// Re-parse params for option selection. A malformed envelope produced a
	// denial above; selectPermissionOptionID still needs to pick a rejection
	// option from whatever option list (if any) the agent supplied.
	var params map[string]any
	if envelope.Params != nil {
		_ = json.Unmarshal(envelope.Params, &params)
	}
	optionID := selectPermissionOptionID(params, approved)
	var outcome map[string]any
	if optionID != "" {
		outcome = map[string]any{"outcome": "selected", "optionId": optionID}
	} else {
		// No matching option offered — fall back to cancelled so the agent
		// can finish the turn instead of waiting on us.
		outcome = map[string]any{"outcome": "cancelled"}
	}
	if reason != "" {
		outcome["_meta"] = map[string]any{"reason": reason}
	}

	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      envelope.ID,
		"result":  map[string]any{"outcome": outcome},
	}
	if err := t.send(response); err != nil {
		fmt.Fprintf(os.Stderr, "acpcore: send permission response failed (agent may hang): %v\n", err)
	}
}

// decidePermission produces (approved, reason) for an ACP permission request
// given raw params bytes. Empty or malformed JSON yields a denial (B15)
// rather than the previous silent-approve default. The handler is consulted
// only after a successful unmarshal of non-empty params.
func decidePermission(handler permissionHandler, rawParams json.RawMessage) (bool, string) {
	if len(rawParams) == 0 {
		return false, "empty permission params"
	}
	var params map[string]any
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return false, fmt.Sprintf("malformed permission params: %v", err)
	}
	if handler == nil {
		return true, ""
	}
	decision := handler(params)
	if d, _ := decision["decision"].(string); d == "deny" {
		reason, _ := decision["reason"].(string)
		return false, reason
	}
	return true, ""
}

// selectPermissionOptionID picks an optionId from the params.options[] list
// whose `kind` matches the requested polarity. Falls back to the first option
// matching the polarity, then the first option of any kind.
func selectPermissionOptionID(params map[string]any, approved bool) string {
	rawOpts, _ := params["options"].([]any)
	wantKinds := []string{"allow_once", "allow_always"}
	if !approved {
		wantKinds = []string{"reject_once", "reject_always"}
	}
	var fallbackOptionID string
	for _, raw := range rawOpts {
		opt, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := opt["optionId"].(string)
		if id == "" {
			continue
		}
		if fallbackOptionID == "" {
			fallbackOptionID = id
		}
		kind, _ := opt["kind"].(string)
		for _, want := range wantKinds {
			if kind == want {
				return id
			}
		}
	}
	return fallbackOptionID
}

func translatePermissionRequest(req map[string]any) analyzer.PermissionRequest {
	out := analyzer.PermissionRequest{}
	if kind, ok := req["kind"].(string); ok {
		switch kind {
		case "read":
			out.Kind = analyzer.PermissionKindRead
		case "url":
			out.Kind = analyzer.PermissionKindURL
		case "shell":
			out.Kind = analyzer.PermissionKindShell
		case "mcp":
			out.Kind = analyzer.PermissionKindMCPTool
		case "custom_tool":
			out.Kind = analyzer.PermissionKindCustomTool
		default:
			out.Kind = analyzer.PermissionKind(kind)
		}
	}
	if path, ok := req["path"].(string); ok {
		out.Path = &path
	}
	if possible, ok := req["possible_paths"].([]any); ok {
		for _, candidatePath := range possible {
			if s, ok := candidatePath.(string); ok {
				out.PossiblePaths = append(out.PossiblePaths, s)
			}
		}
	}
	if readOnly, ok := req["read_only"].(bool); ok {
		out.ReadOnly = &readOnly
	}
	if hasRedir, ok := req["has_write_file_redirection"].(bool); ok {
		out.HasWriteFileRedirection = &hasRedir
	}
	if fullText, ok := req["full_command_text"].(string); ok {
		out.FullCommandText = &fullText
	}
	if commands, ok := req["commands"].([]any); ok {
		for _, command := range commands {
			cmd, ok := command.(map[string]any)
			if !ok {
				continue
			}
			shell := analyzer.ShellCommand{}
			if identifier, ok := cmd["identifier"].(string); ok {
				shell.Identifier = identifier
			}
			if readOnly, ok := cmd["read_only"].(bool); ok {
				shell.ReadOnly = readOnly
			}
			out.Commands = append(out.Commands, shell)
		}
	}
	return out
}

func extractSessionID(rawResponse json.RawMessage) string {
	if len(rawResponse) == 0 {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return ""
	}
	if sid, ok := parsed["sessionId"].(string); ok {
		return sid
	}
	if sid, ok := parsed["session_id"].(string); ok {
		return sid
	}
	return ""
}

// pickAvailableModelID returns the modelId from session/new's
// result.models.availableModels[] that matches `preferred`. If `preferred`
// isn't present, tries each fallback in order. Returns ok=false when no
// match is found so the caller skips set_model.
func pickAvailableModelID(rawResponse json.RawMessage, preferred string, fallbacks []string) (string, bool) {
	if preferred == "" {
		return "", false
	}
	var parsed struct {
		Models struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return "", false
	}
	// Build candidate list: preferred first, then fallbacks.
	candidates := make([]string, 0, 1+len(fallbacks))
	candidates = append(candidates, preferred)
	candidates = append(candidates, fallbacks...)
	for _, want := range candidates {
		for _, m := range parsed.Models.AvailableModels {
			if m.ModelID == want {
				return m.ModelID, true
			}
		}
	}
	return "", false
}

// pickReadOnlyModeID picks a modeId that minimizes side effects. Recognizes
// "plan" (claude-code-acp), "read-only" (codex-acp). Returns ok=false when
// the agent doesn't advertise modes or none of the recognized ids appear.
func pickReadOnlyModeID(rawResponse json.RawMessage) (string, bool) {
	var parsed struct {
		Modes struct {
			AvailableModes []struct {
				ID string `json:"id"`
			} `json:"availableModes"`
		} `json:"modes"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return "", false
	}
	preferred := []string{"plan", "read-only", "readonly"}
	for _, want := range preferred {
		for _, m := range parsed.Modes.AvailableModes {
			if m.ID == want {
				return m.ID, true
			}
		}
	}
	return "", false
}

func extractStopReason(rawResponse json.RawMessage) string {
	if len(rawResponse) == 0 {
		return ""
	}
	var parsed struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return ""
	}
	return parsed.StopReason
}
