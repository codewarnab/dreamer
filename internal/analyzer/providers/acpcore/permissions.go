package acpcore

import (
	"encoding/json"
	"fmt"
	"os"

	"dreamer/internal/analyzer"
)

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
