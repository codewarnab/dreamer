package handlers

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"sort"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/config"
	"dreamer/internal/logging"
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
	// Remediation is the operator-facing setup/recovery hint for this provider.
	// Sourced from config.RemediationMessage. Empty only when the provider has
	// no registered defaults (should not happen in practice).
	Remediation string `json:"remediation"`
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
			var st *state.State
			var err error
			if deps.StateCache != nil {
				st, err = deps.StateCache.GetState(cfg.Daemon.OutputRoot, p.Name)
			} else {
				st, err = state.Load(cfg.Daemon.OutputRoot, p.Name)
			}
			if err != nil || st == nil {
				continue
			}
			for id, usage := range st.ProviderUsage {
				agg := merged[id]
				agg.Runs += usage.Runs
				agg.TotalTokens += usage.TotalTokens
				agg.Failures += usage.Failures
				agg.Timeouts += usage.Timeouts
				if usage.LastSuccessUTC.After(agg.LastSuccessUTC) {
					agg.LastSuccessUTC = usage.LastSuccessUTC
					// Last-success wins: clear any stale error from a prior
					// failing run on a different project.
					agg.LastError = usage.LastError
				} else if agg.LastSuccessUTC.IsZero() && usage.LastError != "" {
					agg.LastError = usage.LastError
				}
				merged[id] = agg
			}
		}

		now := time.Now().UTC()
		out := providersResponse{Providers: make([]ProviderHealth, 0, len(merged))}
		for id, usage := range merged {
			model := ""
			if cfg.Providers != nil {
				model = cfg.Providers[id].Model
			}
			if model == "" {
				model = config.DefaultModelFor(id)
			}
			lastSuccess := ""
			if !usage.LastSuccessUTC.IsZero() {
				lastSuccess = usage.LastSuccessUTC.UTC().Format(time.RFC3339)
			}
			healthy := usage.LastError == "" &&
				usage.Runs > 0 &&
				!usage.LastSuccessUTC.IsZero() &&
				now.Sub(usage.LastSuccessUTC.UTC()) < providerHealthWindow
			out.Providers = append(out.Providers, ProviderHealth{
				ID:             id,
				Model:          model,
				Runs:           usage.Runs,
				TotalTokens:    usage.TotalTokens,
				LastSuccessUTC: lastSuccess,
				LastError:      usage.LastError,
				Failures:       usage.Failures,
				Timeouts:       usage.Timeouts,
				Healthy:        healthy,
				Remediation:    config.RemediationMessage(id),
			})
		}
		sort.Slice(out.Providers, func(i, j int) bool {
			return out.Providers[i].ID < out.Providers[j].ID
		})

		writeJSON(w, http.StatusOK, out)
	}
}

// providerMetaEntry carries static metadata for one registered provider.
// Used by GET /api/provider-meta.
type providerMetaEntry struct {
	ID             string   `json:"id"`
	DisplayName    string   `json:"display_name"`
	Order          int      `json:"order"`
	Models         []string `json:"models"`
	DefaultModel   string   `json:"default_model"`
	DefaultSandbox string   `json:"default_sandbox"`
	Remediation    string   `json:"remediation"`
	// ModelSource indicates how the models list was populated:
	//   "live"   — served from the in-memory cache (fetched from a running provider)
	//   "static" — served from defaults.AllModels (no cache entry yet)
	ModelSource string `json:"model_source"`
}

type providerMetaResponse struct {
	Providers []providerMetaEntry `json:"providers"`
}

// ProviderMeta returns GET /api/provider-meta. It assembles per-provider
// display metadata (ordered model list, display name, remediation hint) from
// config.AllProviderDefaults and analyzer.RegisteredProviderMeta so the UI
// model datalist never drifts from defaults.go.
//
// For providers that implement analyzer.ModelLister and are currently running,
// the models list is replaced with the live result (cached for modelListCacheTTL).
// All other providers fall back to config.LookupProviderDefaults AllModels.
//
// This endpoint is separately cacheable from /api/providers (which returns
// run-health, not static metadata) and is called once on page load.
func ProviderMeta(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		metas := analyzer.RegisteredProviderMeta()
		entries := make([]providerMetaEntry, 0, len(metas))
		for _, m := range metas {
			id := string(m.ID)
			defaults, _ := config.LookupProviderDefaults(m.ID)
			models, modelSource := enrichWithLiveModels(r.Context(), id, defaults.AllModels, deps)
			entries = append(entries, providerMetaEntry{
				ID:             id,
				DisplayName:    m.DisplayName,
				Order:          m.Order,
				Models:         models,
				DefaultModel:   defaults.DefaultModel,
				DefaultSandbox: defaults.DefaultSandbox,
				Remediation:    config.RemediationMessage(id),
				ModelSource:    modelSource,
			})
		}

		writeJSON(w, http.StatusOK, providerMetaResponse{Providers: entries})
	}
}

// modelListFetchTimeout is the per-provider budget for the background
// model-list fetch (Start + ListModels combined). ACP providers spawn a
// child process and run an initialize + session/new round-trip, which can
// take several seconds on a cold start. 30 seconds is generous enough for
// authenticated CLI tools that need network; the goroutine is fire-and-forget
// so this timeout never blocks an HTTP response.
const modelListFetchTimeout = 30 * time.Second

// enrichWithLiveModels returns the cached model list for providerID when one
// is available, otherwise fires a background goroutine to populate the cache
// and immediately returns the static fallback. This keeps /api/provider-meta
// non-blocking regardless of how long the provider takes to start.
//
// The second return value is the model source:
//   - "live"   — list was served from the in-memory cache (a prior live fetch succeeded)
//   - "static" — no cache entry yet; static fallback was returned for this request
//
// The background goroutine uses deps.ShutdownCtx (when set) so it is
// cancelled on daemon shutdown rather than running past process exit.
//
// Steps: (1) cache hit → return live list, (2) if provider implements
// ModelLister → launch background fetch, return fallback for this request,
// (3) next request after fetch completes → cache hit returns live list.
func enrichWithLiveModels(_ context.Context, providerID string, fallback []string, deps Deps) ([]string, string) {
	if deps.ModelListCache == nil {
		return fallback, "static"
	}

	// Step 1: cache hit — return immediately.
	if cached := deps.ModelListCache.Get(providerID); cached != nil {
		return cached, "live"
	}

	// Step 2: check if the provider implements ModelLister before spawning.
	factory, ok := analyzer.LookupProvider(analyzer.ProviderID(providerID))
	if !ok {
		return fallback, "static"
	}

	cfg := deps.Config()
	var block config.ProviderBlock
	if cfg != nil && cfg.Providers != nil {
		block = cfg.Providers[providerID]
	}

	var sandboxCfg config.SandboxConfig
	if cfg != nil {
		sandboxCfg = cfg.Sandbox
	}
	provCfg := analyzer.ProviderConfigFromBlock(providerID, block, sandboxCfg)
	// Disable sandbox for the model-listing probe. This is a lightweight
	// capability query, not an analysis run. Running prepare() / postStart()
	// from a background goroutine triggers Windows ACL mutations that race
	// with the main process heap and cause STATUS_HEAP_CORRUPTION (0xC0000374).
	provCfg.Sandbox = "false"

	prov, err := factory(provCfg)
	if err != nil {
		return fallback, "static"
	}

	lister, ok := prov.(analyzer.ModelLister)
	if !ok {
		// Provider doesn't implement ModelLister — static fallback.
		return fallback, "static"
	}

	// Step 3: launch a background fetch so this request returns immediately.
	// The fetch populates the cache; the next /api/provider-meta request will
	// get the live list from the cache hit in Step 1.
	bgCtx := context.Background()
	if deps.ShutdownCtx != nil {
		bgCtx = deps.ShutdownCtx
	}
	cacheDir := ""
	if deps.CacheDir != nil {
		cacheDir = deps.CacheDir()
	}
	go func() {
		fetchCtx, cancel := context.WithTimeout(bgCtx, modelListFetchTimeout)
		defer cancel()
		defer prov.Close()

		if err := prov.Start(fetchCtx); err != nil {
			return
		}
		models, err := lister.ListModels(fetchCtx)
		if err != nil || len(models) == 0 {
			return
		}
		deps.ModelListCache.Set(providerID, models)
		if cacheDir != "" {
			if saveErr := deps.ModelListCache.Save(cacheDir); saveErr != nil {
				deps.Logger.Warn("model list cache save failed",
					logging.Any("dir", cacheDir),
					logging.Any("err", saveErr),
				)
			}
		}
	}()

	return fallback, "static"
}

// ProviderTest handles POST /api/providers/{id}/test.
// It builds a minimal session against the named provider using the current
// config, runs a one-turn smoke prompt, and reports pass/fail with latency.
// Always returns HTTP 200 on test completion (pass or fail); non-2xx only for
// request-level errors (bad ID, method not allowed).
func ProviderTest(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		id := providerIDFromPath(r.URL.Path)
		if id == "" {
			writeJSONError(w, http.StatusBadRequest, "unknown provider id")
			return
		}

		cfg := deps.Config()
		if cfg == nil {
			writeJSONError(w, http.StatusInternalServerError, "config unavailable")
			return
		}

		factory, ok := analyzer.LookupProvider(analyzer.ProviderID(id))
		if !ok {
			writeJSONError(w, http.StatusServiceUnavailable, "provider not registered")
			return
		}

		block := config.ProviderBlock{}
		if cfg.Providers != nil {
			if b, exists := cfg.Providers[id]; exists {
				block = b
			}
		}

		provCfg := analyzer.ProviderConfigFromBlock(id, block, cfg.Sandbox)
		// Force max-turns = 1: smoke test must not spin.
		provCfg.MaxTurns = 1

		prov, err := factory(provCfg)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":       false,
				"error":    err.Error(),
				"category": providerErrorCategory(err.Error()),
			})
			return
		}

		ctx := r.Context()
		start := time.Now()
		if startErr := prov.Start(ctx); startErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":       false,
				"error":    startErr.Error(),
				"category": providerErrorCategory(startErr.Error()),
			})
			return
		}
		defer prov.Close()

		// smokeTestPrompt is a minimal one-turn instruction that verifies
		// the provider is reachable and authenticated. Trivially short
		// response to minimise token cost and latency.
		const smokeTestPrompt = "Reply with the single word OK and nothing else."
		sess, sessErr := prov.NewSession(ctx, analyzer.SessionConfig{
			WorkingDirectory: os.TempDir(),
		})
		if sessErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":       false,
				"error":    sessErr.Error(),
				"category": providerErrorCategory(sessErr.Error()),
			})
			return
		}
		defer sess.Close()

		// smokeTestTimeout: 30 seconds is ample for a one-turn ping and
		// is short enough that a stalled provider doesn't block the UI.
		const smokeTestTimeout = 30 * time.Second
		_, runErr := sess.Run(ctx, smokeTestPrompt, smokeTestTimeout)
		latencyMS := time.Since(start).Milliseconds()
		if runErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":         false,
				"error":      runErr.Error(),
				"category":   providerErrorCategory(runErr.Error()),
				"latency_ms": latencyMS,
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"latency_ms": latencyMS,
		})
	}
}

// RouteProviders dispatches /api/providers/{id}/test and similar sub-paths.
// Anything not matching a known sub-route returns 404.
func RouteProviders(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only /api/providers/{id}/test is currently handled.
		id := providerIDFromPath(r.URL.Path)
		if id == "" {
			http.NotFound(w, r)
			return
		}
		ProviderTest(deps)(w, r)
	})
}

// providerIDFromPath extracts the provider id from
// /api/providers/{id}/test, returning "" when the path doesn't match.
func providerIDFromPath(path string) string {
	// Expected: /api/providers/<id>/test
	const prefix = "/api/providers/"
	const suffix = "/test"
	if len(path) <= len(prefix)+len(suffix) {
		return ""
	}
	if path[:len(prefix)] != prefix {
		return ""
	}
	rest := path[len(prefix):]
	if len(rest) <= len(suffix) || rest[len(rest)-len(suffix):] != suffix {
		return ""
	}
	id := rest[:len(rest)-len(suffix)]
	if id == "" {
		return ""
	}
	return id
}

// providerErrorCategory classifies a provider error string into a short
// human-readable label. Mirrors the client-side categoriseProviderError
// table in settings.js. Both must be updated together when new patterns
// are added.
func providerErrorCategory(msg string) string {
	for _, cat := range providerErrorCategories {
		if cat.re.MatchString(msg) {
			return cat.label
		}
	}
	return "error"
}

// providerErrorCategory entry — pattern ordered most-specific first.
type errorCategory struct {
	re    *regexp.Regexp
	label string
}

// providerErrorCategories is the ordered pattern table used by
// providerErrorCategory. Entries are compiled once at init.
var providerErrorCategories = []errorCategory{
	{re: regexp.MustCompile(`(?i)rate.?limit|429|quota.?exceeded`), label: "rate limited"},
	{re: regexp.MustCompile(`(?i)not.?found.?in.?PATH|binary.*not found|not installed`), label: "not installed"},
	{re: regexp.MustCompile(`(?i)all \d+ output lines failed to parse|provider schema change`), label: "version mismatch"},
	{re: regexp.MustCompile(`(?i)permission denied|auth|unauthori[zs]ed|invalid.*key|api key`), label: "auth failure"},
	{re: regexp.MustCompile(`(?i)timeout|deadline|context canceled`), label: "timed out"},
	{re: regexp.MustCompile(`(?i)no assistant content`), label: "empty response"},
}
