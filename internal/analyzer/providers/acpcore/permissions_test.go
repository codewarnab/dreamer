package acpcore

import (
	"encoding/json"
	"testing"

	"dreamer/internal/analyzer"
)

// --- decidePermission ---

// B15: malformed permission request JSON must deny rather than silently
// approving (the previous handler discarded the unmarshal error and fell
// through to approved=true).
func TestDecidePermissionDeniesOnMalformedJSON(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, json.RawMessage(`{not-json`))
	if approved {
		t.Fatalf("malformed params must be denied")
	}
	if reason == "" {
		t.Fatalf("denial must carry a reason")
	}
}

func TestDecidePermissionAppliesHandlerDeny(t *testing.T) {
	handler := func(p map[string]any) map[string]any {
		return map[string]any{"decision": "deny", "reason": "outside root"}
	}
	approved, reason := decidePermission(handler, json.RawMessage(`{"kind":"read"}`))
	if approved {
		t.Fatalf("handler deny must propagate")
	}
	if reason != "outside root" {
		t.Fatalf("reason = %q, want %q", reason, "outside root")
	}
}

func TestDecidePermissionDeniesOnEmptyParams(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, nil)
	if approved {
		t.Fatalf("empty params must be denied")
	}
	if reason == "" {
		t.Fatalf("denial must carry a reason")
	}
}

func TestDecidePermissionDefaultsApprovedOnEmptyDecision(t *testing.T) {
	handler := func(p map[string]any) map[string]any { return map[string]any{} }
	approved, reason := decidePermission(handler, json.RawMessage(`{"kind":"read"}`))
	if !approved {
		t.Fatalf("empty decision must default to approve")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func TestDecidePermissionNilHandlerApproves(t *testing.T) {
	approved, reason := decidePermission(nil, json.RawMessage(`{"kind":"read"}`))
	if !approved {
		t.Fatalf("nil handler should approve")
	}
	if reason != "" {
		t.Fatalf("reason = %q, want empty", reason)
	}
}

func TestPermissionHandlerForRoutesBySessionID(t *testing.T) {
	tr := &transport{}
	tr.registerPermissionHandler("session-a", func(map[string]any) map[string]any {
		return map[string]any{"decision": "allow"}
	})
	tr.registerPermissionHandler("session-b", func(map[string]any) map[string]any {
		return map[string]any{"decision": "deny", "reason": "session-b denied"}
	})
	t.Cleanup(func() {
		tr.unregisterPermissionHandler("session-a")
		tr.unregisterPermissionHandler("session-b")
	})

	handler := tr.permissionHandlerFor(map[string]any{"sessionId": "session-b"})
	if handler == nil {
		t.Fatal("expected handler for session-b")
	}
	approved, reason := decidePermission(handler, json.RawMessage(`{"sessionId":"session-b","kind":"read"}`))
	if approved {
		t.Fatalf("session-b handler should deny")
	}
	if reason != "session-b denied" {
		t.Fatalf("reason = %q, want session-b denied", reason)
	}
}

func TestPermissionHandlerForUnknownSessionIsNil(t *testing.T) {
	tr := &transport{}
	tr.registerPermissionHandler("session-a", func(map[string]any) map[string]any {
		return map[string]any{"decision": "deny"}
	})

	if got := tr.permissionHandlerFor(map[string]any{"sessionId": "missing"}); got != nil {
		t.Fatal("expected no handler for unknown session")
	}
}

// --- selectPermissionOptionID ---

func TestSelectPermissionOptionIDApproved(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "reject_once"},
			map[string]any{"optionId": "opt2", "kind": "allow_once"},
			map[string]any{"optionId": "opt3", "kind": "allow_always"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "opt2" {
		t.Fatalf("got %q, want opt2", got)
	}
}

func TestSelectPermissionOptionIDRejected(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "allow_once"},
			map[string]any{"optionId": "opt2", "kind": "reject_once"},
			map[string]any{"optionId": "opt3", "kind": "reject_always"},
		},
	}
	got := selectPermissionOptionID(params, false)
	if got != "opt2" {
		t.Fatalf("got %q, want opt2", got)
	}
}

func TestSelectPermissionOptionIDFallbackToFirst(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "opt1", "kind": "unknown"},
			map[string]any{"optionId": "opt2", "kind": "unknown"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "opt1" {
		t.Fatalf("got %q, want opt1 (fallback to first)", got)
	}
}

func TestSelectPermissionOptionIDNoOptions(t *testing.T) {
	got := selectPermissionOptionID(map[string]any{}, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDSkipsNonMap(t *testing.T) {
	params := map[string]any{
		"options": []any{"not-a-map", 42},
	}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDSkipsEmptyID(t *testing.T) {
	params := map[string]any{
		"options": []any{
			map[string]any{"optionId": "", "kind": "allow_once"},
		},
	}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestSelectPermissionOptionIDNonArrayOptions(t *testing.T) {
	params := map[string]any{"options": "not-an-array"}
	got := selectPermissionOptionID(params, true)
	if got != "" {
		t.Fatalf("non-array: got %q, want empty", got)
	}
}

// --- translatePermissionRequest ---

func TestTranslatePermissionRequestAllKinds(t *testing.T) {
	tests := []struct {
		kind string
		want analyzer.PermissionKind
	}{
		{"read", analyzer.PermissionKindRead},
		{"url", analyzer.PermissionKindURL},
		{"shell", analyzer.PermissionKindShell},
		{"mcp", analyzer.PermissionKindMCPTool},
		{"custom_tool", analyzer.PermissionKindCustomTool},
		{"unknown-kind", analyzer.PermissionKind("unknown-kind")},
	}
	for _, tt := range tests {
		req := translatePermissionRequest(map[string]any{"kind": tt.kind})
		if req.Kind != tt.want {
			t.Errorf("kind=%q: got %q, want %q", tt.kind, req.Kind, tt.want)
		}
	}
}

func TestTranslatePermissionRequestReadKind(t *testing.T) {
	req := map[string]any{"kind": "read", "path": "/foo/bar"}
	pr := translatePermissionRequest(req)
	if pr.Kind != analyzer.PermissionKindRead {
		t.Fatalf("Kind = %q, want read", pr.Kind)
	}
	if pr.Path == nil || *pr.Path != "/foo/bar" {
		t.Fatalf("Path = %v, want /foo/bar", pr.Path)
	}
}

func TestTranslatePermissionRequestShellKind(t *testing.T) {
	req := map[string]any{
		"kind":                       "shell",
		"full_command_text":          "ls -la",
		"read_only":                  true,
		"has_write_file_redirection": false,
	}
	pr := translatePermissionRequest(req)
	if pr.Kind != analyzer.PermissionKindShell {
		t.Fatalf("Kind = %q, want shell", pr.Kind)
	}
	if pr.FullCommandText == nil || *pr.FullCommandText != "ls -la" {
		t.Fatalf("FullCommandText = %v, want ls -la", pr.FullCommandText)
	}
	if pr.ReadOnly == nil || !*pr.ReadOnly {
		t.Fatalf("ReadOnly = %v, want true", pr.ReadOnly)
	}
}

func TestTranslatePermissionRequestPossiblePaths(t *testing.T) {
	req := map[string]any{
		"kind":           "read",
		"possible_paths": []any{"/a.go", "/b.go", 42},
	}
	pr := translatePermissionRequest(req)
	if len(pr.PossiblePaths) != 2 {
		t.Fatalf("PossiblePaths len = %d, want 2", len(pr.PossiblePaths))
	}
}

func TestTranslatePermissionRequestCommands(t *testing.T) {
	req := map[string]any{
		"commands": []any{
			map[string]any{"identifier": "git", "read_only": true},
			map[string]any{"identifier": "rm", "read_only": false},
			"not-a-map",
		},
	}
	pr := translatePermissionRequest(req)
	if len(pr.Commands) != 2 {
		t.Fatalf("Commands len = %d, want 2", len(pr.Commands))
	}
	if pr.Commands[0].Identifier != "git" || !pr.Commands[0].ReadOnly {
		t.Fatalf("Commands[0] = %+v", pr.Commands[0])
	}
}

func TestTranslatePermissionRequestEmptyMap(t *testing.T) {
	req := translatePermissionRequest(map[string]any{})
	if req.Kind != "" {
		t.Fatalf("empty map: kind = %q", req.Kind)
	}
}

// --- extractSessionID ---

func TestExtractSessionIDCamelCase(t *testing.T) {
	raw := json.RawMessage(`{"sessionId":"abc-123"}`)
	got := extractSessionID(raw)
	if got != "abc-123" {
		t.Fatalf("got %q, want abc-123", got)
	}
}

func TestExtractSessionIDSnakeCase(t *testing.T) {
	raw := json.RawMessage(`{"session_id":"def-456"}`)
	got := extractSessionID(raw)
	if got != "def-456" {
		t.Fatalf("got %q, want def-456", got)
	}
}

func TestExtractSessionIDEmpty(t *testing.T) {
	if got := extractSessionID(nil); got != "" {
		t.Fatalf("nil: got %q, want empty", got)
	}
	if got := extractSessionID(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("empty obj: got %q, want empty", got)
	}
	if got := extractSessionID(json.RawMessage(`{bad json`)); got != "" {
		t.Fatalf("malformed: got %q, want empty", got)
	}
}

// --- pickAvailableModelID ---

func TestPickAvailableModelIDExactMatch(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"sonnet"},{"modelId":"opus"}]}}`)
	got, ok := pickAvailableModelID(raw, "sonnet", nil)
	if !ok || got != "sonnet" {
		t.Fatalf("got (%q, %v), want (sonnet, true)", got, ok)
	}
}

func TestPickAvailableModelIDFallback(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	got, ok := pickAvailableModelID(raw, "sonnet", []string{"opus", "haiku"})
	if !ok || got != "opus" {
		t.Fatalf("got (%q, %v), want (opus, true)", got, ok)
	}
}

func TestPickAvailableModelIDNoMatch(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	_, ok := pickAvailableModelID(raw, "sonnet", []string{"haiku"})
	if ok {
		t.Fatal("expected ok=false when no match")
	}
}

func TestPickAvailableModelIDEmptyPreferred(t *testing.T) {
	raw := json.RawMessage(`{"models":{"availableModels":[{"modelId":"opus"}]}}`)
	_, ok := pickAvailableModelID(raw, "", nil)
	if ok {
		t.Fatal("expected ok=false for empty preferred")
	}
}

func TestPickAvailableModelIDMalformed(t *testing.T) {
	_, ok := pickAvailableModelID(json.RawMessage(`{bad`), "sonnet", nil)
	if ok {
		t.Fatal("expected ok=false for malformed JSON")
	}
}

// --- pickReadOnlyModeID ---

func TestPickReadOnlyModeIDPlan(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"plan"},{"id":"act"}]}}`)
	got, ok := pickReadOnlyModeID(raw)
	if !ok || got != "plan" {
		t.Fatalf("got (%q, %v), want (plan, true)", got, ok)
	}
}

func TestPickReadOnlyModeIDReadOnly(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"read-only"}]}}`)
	got, ok := pickReadOnlyModeID(raw)
	if !ok || got != "read-only" {
		t.Fatalf("got (%q, %v), want (read-only, true)", got, ok)
	}
}

func TestPickReadOnlyModeIDReadonlyNoHyphen(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"readonly"}]}}`)
	got, ok := pickReadOnlyModeID(raw)
	if !ok || got != "readonly" {
		t.Fatalf("got (%q, %v), want (readonly, true)", got, ok)
	}
}

func TestPickReadOnlyModeIDNoMatch(t *testing.T) {
	raw := json.RawMessage(`{"modes":{"availableModes":[{"id":"act"}]}}`)
	_, ok := pickReadOnlyModeID(raw)
	if ok {
		t.Fatal("expected ok=false when no recognized mode")
	}
}

func TestPickReadOnlyModeIDMalformed(t *testing.T) {
	_, ok := pickReadOnlyModeID(json.RawMessage(`{bad`))
	if ok {
		t.Fatal("expected ok=false for malformed JSON")
	}
}

func TestPickReadOnlyModeIDNoModes(t *testing.T) {
	_, ok := pickReadOnlyModeID(json.RawMessage(`{}`))
	if ok {
		t.Fatal("expected ok=false when no modes key")
	}
}

// --- extractStopReason ---

func TestExtractStopReason(t *testing.T) {
	raw := json.RawMessage(`{"stopReason":"end_turn"}`)
	if got := extractStopReason(raw); got != "end_turn" {
		t.Fatalf("got %q, want end_turn", got)
	}
}

func TestExtractStopReasonEmpty(t *testing.T) {
	if got := extractStopReason(nil); got != "" {
		t.Fatalf("nil: got %q, want empty", got)
	}
	if got := extractStopReason(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("empty: got %q, want empty", got)
	}
	if got := extractStopReason(json.RawMessage(`{bad`)); got != "" {
		t.Fatalf("malformed: got %q, want empty", got)
	}
}
