// Package handlers hosts the HTTP handler funcs for the v1.5 web UI API.
// Handlers receive a minimal Deps slice instead of a *Server back-reference
// so that they live below internal/web in the import graph.
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
)

// Deps is the minimal slice of server context the handlers need. Config is
// a func to return the live (post-overlay-reload) snapshot. Events is the
// shared pub-sub used by lifecycle handlers to publish finding.* events.
type Deps struct {
	Config func() *config.Config
	Events *pipeline.EventBus
}

// Dashboard returns an http.HandlerFunc for GET /api/dashboard.
func Dashboard(deps Deps) http.HandlerFunc {
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
		out := buildDashboard(cfg)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

type dashboardResponse struct {
	Stats               dashboardStats     `json:"stats"`
	Sparkline30d        []state.DaySummary `json:"sparkline_30d"`
	PerCategory         map[string]int     `json:"per_category"`
	TopMistakeProviders []providerCount    `json:"top_mistake_providers"`
	LiveActivity        []any              `json:"live_activity"`
}

type dashboardStats struct {
	ChatsAnalyzedTotal int    `json:"chats_analyzed_total"`
	FindingsOpen       int    `json:"findings_open"`
	FindingsApplied    int    `json:"findings_applied"`
	FindingsDismissed  int    `json:"findings_dismissed"`
	FindingsResolved   int    `json:"findings_resolved"`
	LastRunUTC         string `json:"last_run_utc,omitempty"`
	NextRunUTC         string `json:"next_run_utc,omitempty"`
	TokensWeek         int64  `json:"tokens_week"`
	FailureRatePct     int    `json:"failure_rate_pct"`
	AvgRunSeconds      int    `json:"avg_run_seconds"`
	ProvidersHealthy   string `json:"providers_healthy"`
}

type providerCount struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
}

// buildDashboard aggregates state.json + history.json across every project.
// Exposed (unexported) for testability via the same package.
func buildDashboard(cfg *config.Config) dashboardResponse {
	out := dashboardResponse{
		PerCategory:         map[string]int{},
		TopMistakeProviders: []providerCount{},
		LiveActivity:        []any{},
	}

	var lastRun time.Time
	var totalRunMillisWeighted int64
	var totalRunsForAvg int64
	var totalRunsAllTime int64
	var totalFailures int64
	var weekTokens int64

	sparkline := map[string]state.DaySummary{}
	healthy := map[string]bool{}
	seen := map[string]bool{}

	cutoff7d := time.Now().UTC().AddDate(0, 0, -7)
	cutoff30d := time.Now().UTC().AddDate(0, 0, -30)

	for _, p := range cfg.Projects {
		st, err := state.Load(cfg.Daemon.OutputRoot, p.Name)
		if err != nil || st == nil {
			continue
		}
		out.Stats.ChatsAnalyzedTotal += len(st.ChatHashes)
		if !st.LastRunUTC.IsZero() && st.LastRunUTC.After(lastRun) {
			lastRun = st.LastRunUTC
		}
		for id, pu := range st.ProviderUsage {
			seen[id] = true
			if pu.LastError == "" && pu.Runs > 0 {
				healthy[id] = true
			}
			totalFailures += pu.Failures
			totalRunsAllTime += pu.Runs
		}
		// Lifecycle counts from per-finding state.
		lifecycleTouched := 0
		for _, fs := range st.Findings {
			switch fs.Status {
			case state.FindingStatusApplied:
				out.Stats.FindingsApplied++
				lifecycleTouched++
			case state.FindingStatusDismissed:
				out.Stats.FindingsDismissed++
				lifecycleTouched++
			case state.FindingStatusResolved:
				out.Stats.FindingsResolved++
				lifecycleTouched++
			}
		}
		open := len(st.FindingHashes) - lifecycleTouched
		if open < 0 {
			open = 0
		}
		out.Stats.FindingsOpen += open

		// History accumulation.
		h, err := state.LoadHistory(cfg.Daemon.OutputRoot, p.Name)
		if err != nil || h == nil {
			continue
		}
		for _, d := range h.Days {
			t, perr := time.Parse("2006-01-02", d.Date)
			if perr != nil {
				continue
			}
			if !t.Before(cutoff30d.Truncate(24 * time.Hour)) {
				cur, ok := sparkline[d.Date]
				if !ok {
					cur = state.DaySummary{Date: d.Date, PerCategory: map[string]int{}}
				}
				cur.Runs += d.Runs
				cur.FindingsNew += d.FindingsNew
				cur.FindingsTotal += d.FindingsTotal
				cur.Tokens += d.Tokens
				// Weighted mean across days when summing buckets across projects.
				if cur.Runs > 0 {
					cur.AvgRunMillis = (cur.AvgRunMillis*int64(cur.Runs-d.Runs) + d.AvgRunMillis*int64(d.Runs)) / int64(cur.Runs)
				}
				if cur.PerCategory == nil {
					cur.PerCategory = map[string]int{}
				}
				for cat, n := range d.PerCategory {
					cur.PerCategory[cat] += n
					out.PerCategory[cat] += n
				}
				sparkline[d.Date] = cur
			}
			if !t.Before(cutoff7d.Truncate(24 * time.Hour)) {
				weekTokens += d.Tokens
				totalRunMillisWeighted += d.AvgRunMillis * int64(d.Runs)
				totalRunsForAvg += int64(d.Runs)
			}
		}
	}

	if !lastRun.IsZero() {
		out.Stats.LastRunUTC = lastRun.UTC().Format(time.RFC3339)
		if cfg.Daemon.FrequencySeconds > 0 {
			out.Stats.NextRunUTC = lastRun.UTC().Add(time.Duration(cfg.Daemon.FrequencySeconds) * time.Second).Format(time.RFC3339)
		}
	}
	out.Stats.TokensWeek = weekTokens
	if totalRunsAllTime > 0 {
		out.Stats.FailureRatePct = int((totalFailures * 100) / totalRunsAllTime)
	}
	if totalRunsForAvg > 0 {
		out.Stats.AvgRunSeconds = int(totalRunMillisWeighted / totalRunsForAvg / 1000)
	}
	out.Stats.ProvidersHealthy = fmt.Sprintf("%d/%d", len(healthy), len(seen))

	// Sort sparkline ascending by date and cap to 30 most recent.
	keys := make([]string, 0, len(sparkline))
	for k := range sparkline {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 30 {
		keys = keys[len(keys)-30:]
	}
	out.Sparkline30d = make([]state.DaySummary, 0, len(keys))
	for _, k := range keys {
		out.Sparkline30d = append(out.Sparkline30d, sparkline[k])
	}
	return out
}
