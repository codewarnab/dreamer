package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"dreamer/internal/fsutil"
)

// Settings handles both GET (merged effective config) and PUT (overlay write).
func Settings(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			settingsGet(deps, w, r)
		case http.MethodPut:
			settingsPut(deps, w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func settingsGet(deps Deps, w http.ResponseWriter, r *http.Request) {
	cfg := deps.Config()
	// JSON round-trip clone so sanitization doesn't mutate the live config.
	raw, _ := json.Marshal(cfg)
	var clone map[string]any
	_ = json.Unmarshal(raw, &clone)
	sanitizeProviderEnv(clone)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(clone)
}

// sanitizeProviderEnv redacts env values whose keys hint at secrets.
func sanitizeProviderEnv(m map[string]any) {
	providers, ok := m["providers"].(map[string]any)
	if !ok {
		return
	}
	for _, p := range providers {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		env, ok := block["env"].(map[string]any)
		if !ok {
			continue
		}
		for k := range env {
			uk := strings.ToUpper(k)
			if strings.Contains(uk, "TOKEN") || strings.Contains(uk, "KEY") || strings.Contains(uk, "SECRET") || strings.Contains(uk, "PASSWORD") {
				env[k] = "***redacted***"
			}
		}
	}
}

func settingsPut(deps Deps, w http.ResponseWriter, r *http.Request) {
	if deps.OverlayPath == nil || deps.OverlayPath() == "" {
		http.Error(w, "overlay path not configured", http.StatusServiceUnavailable)
		return
	}
	overlayPath := deps.OverlayPath()

	body, err := readJSONObject(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	existing := map[string]any{}
	if data, err := os.ReadFile(overlayPath); err == nil {
		var current map[string]any
		if err := yaml.Unmarshal(data, &current); err == nil && current != nil {
			existing = current
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		http.Error(w, fmt.Sprintf("read overlay: %v", err), http.StatusInternalServerError)
		return
	}

	mergePartial(existing, body)

	out, err := yaml.Marshal(existing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := fsutil.WriteFileAtomic(overlayPath, out, 0o644); err != nil {
		http.Error(w, fmt.Sprintf("write overlay: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "overlay_path": overlayPath})
}

func readJSONObject(r *http.Request) (map[string]any, error) {
	var m map[string]any
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("decode body: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("empty body")
	}
	return m, nil
}

// mergePartial folds incoming into base. Maps merge recursively; nil values
// in incoming delete keys from base; arrays replace; scalars overwrite.
func mergePartial(base, incoming map[string]any) {
	for k, v := range incoming {
		if v == nil {
			delete(base, k)
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			if existingNested, ok := base[k].(map[string]any); ok {
				mergePartial(existingNested, nested)
				continue
			}
		}
		base[k] = v
	}
}
