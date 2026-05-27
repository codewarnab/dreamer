// Package handlers hosts the HTTP handler funcs for the v1.5 web UI API.
// Handlers receive a minimal Deps slice instead of a *Server back-reference
// so that they live below internal/web in the import graph.
package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
)

// Deps is the minimal slice of server context the handlers need. Config is
// a func to return the live (post-overlay-reload) snapshot. Events is the
// shared pub-sub used by lifecycle handlers to publish finding.* events.
type Deps struct {
	Config         func() *config.Config
	Events         *pipeline.EventBus
	Logger         *logging.Logger
	EnqueueRun     func(projectName string) (runID string, accepted bool, err error)
	OverlayPath    func() string
	RecentActivity func() []pipeline.Event
	RestartDaemon  func() error
	// StateLock serializes state Load→Mutate→Save cycles per project
	// so concurrent lifecycle handlers don't clobber each other.
	StateLock *ProjectLock
	// Jobs holds background job dependencies. When zero-valued, job
	// endpoints return 503.
	Jobs JobDeps
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
		if deps.RecentActivity != nil {
			events := deps.RecentActivity()
			liveActivity := make([]any, 0, len(events))
			for _, e := range events {
				liveActivity = append(liveActivity, map[string]any{
					"type":    e.Type,
					"at":      e.At.UTC().Format(time.RFC3339),
					"payload": e.Payload,
				})
			}
			out.LiveActivity = liveActivity
		}
		writeJSON(w, http.StatusOK, out)
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
	var totalFailuresAllTime int64

	healthy := map[string]bool{}
	seen := map[string]bool{}

	cutoff7d := time.Now().UTC().AddDate(0, 0, -7)
	cutoff30d := time.Now().UTC().AddDate(0, 0, -30)
	aggregatedSparkline := map[string]state.DaySummary{}

	for _, p := range cfg.Projects {
		st, err := state.Load(cfg.Daemon.OutputRoot, p.Name)
		if err != nil || st == nil {
			continue
		}
		out.Stats.ChatsAnalyzedTotal += len(st.ChatHashes)
		if !st.LastRunUTC.IsZero() && st.LastRunUTC.After(lastRun) {
			lastRun = st.LastRunUTC
		}

		healthyProviders, seenProviders, providerFailures, providerRuns := buildProviderHealth(st.ProviderUsage)
		for id := range healthyProviders {
			healthy[id] = true
		}
		for id := range seenProviders {
			seen[id] = true
		}
		totalFailuresAllTime += providerFailures
		totalRunsAllTime += providerRuns

		applied, dismissed, resolved, open := buildLifecycleCounts(st.Findings, st.FindingHashes)
		out.Stats.FindingsApplied += applied
		out.Stats.FindingsDismissed += dismissed
		out.Stats.FindingsResolved += resolved
		out.Stats.FindingsOpen += open

		history, err := state.LoadHistory(cfg.Daemon.OutputRoot, p.Name)
		if err != nil || history == nil {
			continue
		}
		sparkline, weekTokens, weightedRunMillis, runsForAvg, perCategory := buildSparklines(history.Days, cutoff30d, cutoff7d)
		for date, day := range sparkline {
			if cur, ok := aggregatedSparkline[date]; ok {
				cur.Runs += day.Runs
				cur.FindingsNew += day.FindingsNew
				cur.FindingsTotal += day.FindingsTotal
				cur.Tokens += day.Tokens
				if cur.Runs > 0 {
					cur.AvgRunMillis = (cur.AvgRunMillis*int64(cur.Runs-day.Runs) + day.AvgRunMillis*int64(day.Runs)) / int64(cur.Runs)
				}
				for cat, n := range day.PerCategory {
					cur.PerCategory[cat] += n
				}
				aggregatedSparkline[date] = cur
			} else {
				aggregatedSparkline[date] = day
			}
		}
		out.Stats.TokensWeek += weekTokens
		totalRunMillisWeighted += weightedRunMillis
		totalRunsForAvg += runsForAvg
		for cat, n := range perCategory {
			out.PerCategory[cat] += n
		}
	}

	if !lastRun.IsZero() {
		out.Stats.LastRunUTC = lastRun.UTC().Format(time.RFC3339)
		if cfg.Daemon.FrequencySeconds > 0 {
			out.Stats.NextRunUTC = lastRun.UTC().Add(time.Duration(cfg.Daemon.FrequencySeconds) * time.Second).Format(time.RFC3339)
		}
	}
	if totalRunsAllTime > 0 {
		out.Stats.FailureRatePct = int((totalFailuresAllTime * 100) / totalRunsAllTime)
	}
	if totalRunsForAvg > 0 {
		out.Stats.AvgRunSeconds = int(totalRunMillisWeighted / totalRunsForAvg / 1000)
	}
	out.Stats.ProvidersHealthy = fmt.Sprintf("%d/%d", len(healthy), len(seen))

	keys := make([]string, 0, len(aggregatedSparkline))
	for k := range aggregatedSparkline {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 30 {
		keys = keys[len(keys)-30:]
	}
	out.Sparkline30d = make([]state.DaySummary, 0, len(keys))
	for _, k := range keys {
		out.Sparkline30d = append(out.Sparkline30d, aggregatedSparkline[k])
	}
	return out
}

// buildProviderHealth aggregates provider health across a project's state.
// Returns healthy providers, seen providers, total failures, and total runs.
func buildProviderHealth(usage map[string]state.ProviderUsage) (healthy map[string]bool, seen map[string]bool, totalFailures, totalRuns int64) {
	healthy = map[string]bool{}
	seen = map[string]bool{}
	for id, pu := range usage {
		seen[id] = true
		if pu.LastError == "" && pu.Runs > 0 {
			healthy[id] = true
		}
		totalFailures += pu.Failures
		totalRuns += pu.Runs
	}
	return
}

// buildLifecycleCounts computes finding lifecycle counts from per-finding state.
func buildLifecycleCounts(findings map[string]state.FindingState, findingHashes []string) (applied, dismissed, resolved, open int) {
	lifecycleTouched := 0
	for _, findingState := range findings {
		switch findingState.Status {
		case state.FindingStatusApplied:
			applied++
			lifecycleTouched++
		case state.FindingStatusDismissed:
			dismissed++
			lifecycleTouched++
		case state.FindingStatusResolved:
			resolved++
			lifecycleTouched++
		}
	}
	open = len(findingHashes) - lifecycleTouched
	if open < 0 {
		open = 0
	}
	return
}

// buildSparklines aggregates history days into a 30-day sparkline map and
// 7-day token/run totals.
func buildSparklines(days []state.DaySummary, cutoff30d, cutoff7d time.Time) (sparkline map[string]state.DaySummary, weekTokens, weightedRunMillis, runsForAvg int64, perCategory map[string]int) {
	sparkline = map[string]state.DaySummary{}
	perCategory = map[string]int{}
	for _, d := range days {
		parsedDate, parseErr := time.Parse("2006-01-02", d.Date)
		if parseErr != nil {
			continue
		}
		if !parsedDate.Before(cutoff30d.Truncate(24 * time.Hour)) {
			cur, ok := sparkline[d.Date]
			if !ok {
				cur = state.DaySummary{Date: d.Date, PerCategory: map[string]int{}}
			}
			cur.Runs += d.Runs
			cur.FindingsNew += d.FindingsNew
			cur.FindingsTotal += d.FindingsTotal
			cur.Tokens += d.Tokens
			if cur.Runs > 0 {
				cur.AvgRunMillis = (cur.AvgRunMillis*int64(cur.Runs-d.Runs) + d.AvgRunMillis*int64(d.Runs)) / int64(cur.Runs)
			}
			if cur.PerCategory == nil {
				cur.PerCategory = map[string]int{}
			}
			for cat, n := range d.PerCategory {
				cur.PerCategory[cat] += n
				perCategory[cat] += n
			}
			sparkline[d.Date] = cur
		}
		if !parsedDate.Before(cutoff7d.Truncate(24 * time.Hour)) {
			weekTokens += d.Tokens
			weightedRunMillis += d.AvgRunMillis * int64(d.Runs)
			runsForAvg += int64(d.Runs)
		}
	}
	return
}
