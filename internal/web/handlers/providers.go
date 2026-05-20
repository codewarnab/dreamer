package handlers

import (
	"encoding/json"
	"net/http"
	"sort"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/state"
)

// ProviderHealth is one entry in the /api/providers response.
type ProviderHealth struct {
	ID             string `json:"id"`
	Model          string `json:"model"`
	Runs           int64  `json:"runs"`
	TotalTokens    int64  `json:"total_tokens"`
	LastSuccessUTC string `json:"last_success_utc"`
	LastError      string `json:"last_error"`
	Failures       int64  `json:"failures"`
	Timeouts       int64  `json:"timeouts"`
	Healthy        bool   `json:"healthy"`
}

type providersResponse struct {
	Providers []ProviderHealth `json:"providers"`
}

// providerHealthWindow is the recency cutoff for the healthy flag: a
// provider must have a last_success_utc within this window to count.
const providerHealthWindow = 24 * time.Hour

// Providers returns GET /api/providers. It walks every configured project's
// state.json, merges ProviderUsage by provider id, and renders the dashboard
// provider-health table.
func Providers(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}

		merged := map[string]state.ProviderUsage{}
		for _, p := range cfg.Projects {
			st, err := state.Load(cfg.Daemon.OutputRoot, p.Name)
			if err != nil || st == nil {
				continue
			}
			for id, pu := range st.ProviderUsage {
				agg := merged[id]
				agg.Runs += pu.Runs
				agg.TotalTokens += pu.TotalTokens
				agg.Failures += pu.Failures
				agg.Timeouts += pu.Timeouts
				if pu.LastSuccessUTC.After(agg.LastSuccessUTC) {
					agg.LastSuccessUTC = pu.LastSuccessUTC
					// Last-success wins: clear any stale error from a prior
					// failing run on a different project.
					agg.LastError = pu.LastError
				} else if agg.LastSuccessUTC.IsZero() && pu.LastError != "" {
					agg.LastError = pu.LastError
				}
				merged[id] = agg
			}
		}

		now := time.Now().UTC()
		out := providersResponse{Providers: make([]ProviderHealth, 0, len(merged))}
		for id, pu := range merged {
			model := ""
			if cfg.Providers != nil {
				model = cfg.Providers[id].Model
			}
			if model == "" {
				model = config.DefaultModelByProvider[id]
			}
			lastSuccess := ""
			if !pu.LastSuccessUTC.IsZero() {
				lastSuccess = pu.LastSuccessUTC.UTC().Format(time.RFC3339)
			}
			healthy := pu.LastError == "" &&
				pu.Runs > 0 &&
				!pu.LastSuccessUTC.IsZero() &&
				now.Sub(pu.LastSuccessUTC.UTC()) < providerHealthWindow
			out.Providers = append(out.Providers, ProviderHealth{
				ID:             id,
				Model:          model,
				Runs:           pu.Runs,
				TotalTokens:    pu.TotalTokens,
				LastSuccessUTC: lastSuccess,
				LastError:      pu.LastError,
				Failures:       pu.Failures,
				Timeouts:       pu.Timeouts,
				Healthy:        healthy,
			})
		}
		sort.Slice(out.Providers, func(i, j int) bool {
			return out.Providers[i].ID < out.Providers[j].ID
		})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
