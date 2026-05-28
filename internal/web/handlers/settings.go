package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"dreamer/internal/fsutil"
)

// maxSettingsBodyBytes caps the PUT /api/settings request body to prevent
// OOM from oversized payloads. 256 KiB is well above any real overlay config.
const maxSettingsBodyBytes = 256 * 1024

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
	raw, err := json.Marshal(cfg)
	if err != nil {
		http.Error(w, "failed to marshal config", http.StatusInternalServerError)
		return
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		http.Error(w, "failed to clone config", http.StatusInternalServerError)
		return
	}
	sanitizeProviderSecrets(clone)
	writeJSON(w, http.StatusOK, clone)
}

// sanitizeProviderSecrets redacts env values whose keys hint at secrets and top-level passwords.
func sanitizeProviderSecrets(m map[string]any) {
	providers, ok := m["providers"].(map[string]any)
	if !ok {
		return
	}
	for _, p := range providers {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pwd, ok := block["password"].(string); ok && pwd != "" {
			block["password"] = redactedPlaceholder
		}
		env, ok := block["env"].(map[string]any)
		if !ok {
			continue
		}
		for k := range env {
			upperKey := strings.ToUpper(k)
			if strings.Contains(upperKey, "TOKEN") || strings.Contains(upperKey, "KEY") ||
				strings.Contains(upperKey, "SECRET") || strings.Contains(upperKey, "PASSWORD") ||
				strings.Contains(upperKey, "AUTH") || strings.Contains(upperKey, "CREDENTIAL") ||
				strings.Contains(upperKey, "BEARER") || strings.Contains(upperKey, "PRIVATE") {
				env[k] = redactedPlaceholder
			}
		}
		// Strip userinfo from URL fields (e.g. https://user:pass@host).
		for _, urlKey := range []string{"base_url", "cli_url"} {
			if u, ok := block[urlKey].(string); ok && u != "" {
				if parsed, err := url.Parse(u); err == nil && parsed.User != nil {
					parsed.User = nil
					block[urlKey] = parsed.String()
				}
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

	// Cap request body so a loopback caller can't OOM the daemon by
	// streaming a multi-GB payload into json+yaml decoders. 256 KiB is
	// well over any plausible overlay (max real config is ~few KiB).
	r.Body = http.MaxBytesReader(w, r.Body, maxSettingsBodyBytes)
	body, err := readJSONObject(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if providers, ok := body["providers"].(map[string]any); ok {
		for id, p := range providers {
			block, ok := p.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"command", "env", "base_url", "cli_url"} {
				if _, exists := block[key]; exists {
					http.Error(w, fmt.Sprintf("modifying %s for provider %q is not allowed", key, id), http.StatusBadRequest)
					return
				}
			}
		}
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

	// Strip redacted placeholders so a GET→PUT round-trip cannot write
	// "***redacted***" into the overlay, silently corrupting real secrets.
	stripRedactedPlaceholders(body)

	mergePartial(existing, body)

	out, err := yaml.Marshal(existing)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := fsutil.WriteFileAtomic(overlayPath, out, fsutil.FilePerms); err != nil {
		http.Error(w, fmt.Sprintf("write overlay: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "overlay_path": overlayPath})
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

const redactedPlaceholder = "***redacted***"

// stripRedactedPlaceholders removes any provider env value or password
// equal to the redacted placeholder that settingsGet uses in its response.
// Without this, a GET→PUT round-trip writes the placeholder into the
// overlay, silently corrupting whatever real secret was there.
func stripRedactedPlaceholders(body map[string]any) {
	providers, ok := body["providers"].(map[string]any)
	if !ok {
		return
	}
	for _, p := range providers {
		block, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pwd, ok := block["password"].(string); ok && pwd == redactedPlaceholder {
			delete(block, "password")
		}
		env, ok := block["env"].(map[string]any)
		if !ok {
			continue
		}
		for k, v := range env {
			if s, ok := v.(string); ok && s == redactedPlaceholder {
				delete(env, k)
			}
		}
	}
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
