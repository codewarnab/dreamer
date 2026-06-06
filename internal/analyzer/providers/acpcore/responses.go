package acpcore

import "encoding/json"

// --- ACP response parsers ---
//
// These helpers parse raw JSON-RPC result payloads from session/new and
// session/prompt. They live next to their sole caller (session.Run) so
// the call→parse relationship is obvious.

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

// extractAvailableModelIDs returns all modelId values from a session/new
// result's models.availableModels[] array. Returns nil when the field is
// absent, empty, or the payload cannot be parsed — callers must treat a nil
// return as "no models advertised" and fall back to their static list.
//
// This is the pure-function counterpart to pickAvailableModelID: that helper
// selects one model by preference; this one returns the full set so the UI
// model picker can show every option the agent supports.
func extractAvailableModelIDs(rawResponse json.RawMessage) []string {
	if len(rawResponse) == 0 {
		return nil
	}
	var parsed struct {
		Models struct {
			AvailableModels []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rawResponse, &parsed); err != nil {
		return nil
	}
	if len(parsed.Models.AvailableModels) == 0 {
		return nil
	}
	ids := make([]string, 0, len(parsed.Models.AvailableModels))
	for _, m := range parsed.Models.AvailableModels {
		if m.ModelID != "" {
			ids = append(ids, m.ModelID)
		}
	}
	return ids
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
