package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dreamer/internal/analyzer/transport"
	"dreamer/internal/config"
	"dreamer/internal/state"
	"dreamer/internal/web/apply"
)

// FindingView is the per-finding shape returned by the list and detail
// endpoints. The list endpoint populates the human-readable summary +
// category from todos.md and reconciles status against state.Findings.
// Rich guardrail/apply payloads live in the rule pack output but are
// not currently re-serialized into todos.md; they remain available via
// the detail endpoint's query-string handoff used by the SPA.
type FindingView struct {
	Hash        string  `json:"hash"`
	Category    string  `json:"category"`
	Summary     string  `json:"summary"`
	Confidence  float64 `json:"confidence,omitempty"`
	Status      string  `json:"status"`
	AppliedAt   string  `json:"applied_at,omitempty"`
	DismissedAt string  `json:"dismissed_at,omitempty"`
	ResolvedAt  string  `json:"resolved_at,omitempty"`
	Recurred    bool    `json:"recurred"`
	LastSeenUTC string  `json:"last_seen_utc,omitempty"`
}

var (
	findingMarkerRe = regexp.MustCompile(`<!--\s*dreamer:finding:([0-9a-fA-F]+)\s*-->`)
	runHeaderRe     = regexp.MustCompile(`^## Run (.+)$`)
	categoryHeadRe  = regexp.MustCompile(`^### (.+)$`)
	bulletStartRe   = regexp.MustCompile(`^- \[[ x]\] (?:\[unverified\] )?(.*)$`)
)

// todosEntry is one finding occurrence in todos.md: which run section it
// appeared in, what category heading preceded it, and the bullet text.
type todosEntry struct {
	Hash         string
	Category     string
	Summary      string
	RunTimestamp string
}

// parseTodosLatestRun walks todos.md once and returns:
//   - latest: set of hashes that appear in the most recent `## Run` section
//   - all:    every entry across every run, in file order (oldest first;
//     newer runs are appended to the bottom of the file by GenerateTodos)
//
// The format produced by output.GenerateTodos is:
//
//	## Run <RFC3339>
//	### <Category>
//	- [ ] <summary line> [ — guardrail: ... ]
//	    (optional indented snippet / evidence lines)
//	    <!-- dreamer:finding:<hex> -->
func parseTodosLatestRun(path string) (latest map[string]bool, all []todosEntry, err error) {
	latest = map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return latest, nil, nil
		}
		return nil, nil, err
	}
	defer f.Close()

	var (
		currentRun      string
		latestRun       string
		currentCategory string
		pendingSummary  string
	)
	sc := transport.NewFileScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if m := runHeaderRe.FindStringSubmatch(line); m != nil {
			currentRun = strings.TrimSpace(m[1])
			// Strip optional [runID] suffix ("## Run <ts> [abc12345]").
			if idx := strings.LastIndex(currentRun, " ["); idx >= 0 {
				currentRun = strings.TrimSpace(currentRun[:idx])
			}
			latestRun = currentRun // newest run wins; file order is oldest-first.
			currentCategory = ""
			pendingSummary = ""
			continue
		}
		if m := categoryHeadRe.FindStringSubmatch(line); m != nil {
			currentCategory = strings.TrimSpace(m[1])
			pendingSummary = ""
			continue
		}
		if m := bulletStartRe.FindStringSubmatch(line); m != nil {
			pendingSummary = strings.TrimSpace(m[1])
			// Strip the " — guardrail: tool/rule." suffix that
			// renderMistakeLine appends so the UI gets a clean
			// human summary.
			if idx := strings.Index(pendingSummary, " — guardrail:"); idx >= 0 {
				pendingSummary = strings.TrimSpace(pendingSummary[:idx])
			}
			continue
		}
		if m := findingMarkerRe.FindStringSubmatch(line); m != nil {
			hash := strings.ToLower(m[1])
			entry := todosEntry{
				Hash:         hash,
				Category:     currentCategory,
				Summary:      pendingSummary,
				RunTimestamp: currentRun,
			}
			all = append(all, entry)
			pendingSummary = ""
		}
	}
	if err := sc.Err(); err != nil {
		return nil, nil, err
	}
	// Build latest-run set from the final latestRun timestamp.
	for _, e := range all {
		if e.RunTimestamp == latestRun {
			latest[e.Hash] = true
		}
	}
	return latest, all, nil
}

func findProject(cfg *config.Config, name string) (config.ProjectConfig, bool) {
	for _, p := range cfg.Projects {
		if p.Name == name {
			return p, true
		}
	}
	return config.ProjectConfig{}, false
}

// parseProjectNameFromFindings extracts {name} from
// /api/projects/{name}/findings, returning "" if the shape is wrong.
func parseProjectNameFromFindings(urlPath string) string {
	const prefix = "/api/projects/"
	if !strings.HasPrefix(urlPath, prefix) {
		return ""
	}
	tail := strings.TrimPrefix(urlPath, prefix)
	tail = strings.TrimSuffix(tail, "/")
	if !strings.HasSuffix(tail, "/findings") {
		return ""
	}
	name := strings.TrimSuffix(tail, "/findings")
	if name == "" || strings.Contains(name, "/") {
		return ""
	}
	return name
}

// ProjectFindings handles GET /api/projects/{name}/findings.
//
// It reads todos.md to recover the human-readable summary + category for
// each finding hash, reconciles each hash against state.Findings for
// lifecycle (open/applied/dismissed/resolved), and flags `recurred` when
// an applied/resolved hash still appears in the latest run section.
//
// Query params:
//   - status=open|applied|dismissed|resolved (case-insensitive)
//   - category=<heading text> (case-insensitive)
//
// Dismissed findings are hidden by default; they appear only when the
// caller explicitly asks for status=dismissed.
// buildFindingView reconciles a todosEntry against the loaded state and constructs a FindingView.
func buildFindingView(entry todosEntry, st *state.State, latestRunHashes map[string]bool) FindingView {
	status := "open"
	var fs state.FindingState
	var lifecycle bool
	if st != nil {
		fs, lifecycle = st.Findings[entry.Hash]
		if lifecycle && fs.Status != "" {
			status = fs.Status
		}
	}

	view := FindingView{
		Hash:        entry.Hash,
		Category:    entry.Category,
		Summary:     entry.Summary,
		Status:      status,
		LastSeenUTC: entry.RunTimestamp,
	}

	if lifecycle {
		if !fs.AppliedAt.IsZero() {
			view.AppliedAt = fs.AppliedAt.UTC().Format(time.RFC3339)
		}
		if !fs.DismissedAt.IsZero() {
			view.DismissedAt = fs.DismissedAt.UTC().Format(time.RFC3339)
		}
		if !fs.ResolvedAt.IsZero() {
			view.ResolvedAt = fs.ResolvedAt.UTC().Format(time.RFC3339)
		}
		if (status == state.FindingStatusApplied || status == state.FindingStatusResolved) &&
			latestRunHashes[entry.Hash] {
			view.Recurred = true
		}
	}
	return view
}

func ProjectFindings(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := parseProjectNameFromFindings(r.URL.Path)
		if name == "" {
			http.NotFound(w, r)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		if _, ok := findProject(cfg, name); !ok {
			http.NotFound(w, r)
			return
		}
		st, err := state.Load(cfg.Daemon.OutputRoot, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		todosPath := filepath.Join(cfg.Daemon.OutputRoot, name, "todos.md")
		latestRunHashes, allEntries, err := parseTodosLatestRun(todosPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		filterStatus := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
		filterCategory := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))

		// Walk entries (oldest-first in file order; GenerateTodos appends
		// new run sections to the bottom). For dedupe by hash, we keep
		// the most recent occurrence so LastSeenUTC reflects the latest
		// run that mentioned the finding.
		dedupe := map[string]int{}
		out := make([]FindingView, 0, len(allEntries))
		for _, entry := range allEntries {
			view := buildFindingView(entry, st, latestRunHashes)

			// Hide dismissed unless the caller explicitly asks for them.
			if view.Status == state.FindingStatusDismissed && filterStatus != state.FindingStatusDismissed {
				continue
			}
			if filterStatus != "" && filterStatus != view.Status {
				continue
			}
			if filterCategory != "" && strings.ToLower(view.Category) != filterCategory {
				continue
			}

			// Dedupe by hash; overwrite earlier (older) occurrences so
			// LastSeenUTC reflects the most recent run mentioning it.
			if i, seen := dedupe[view.Hash]; seen {
				out[i] = view
				continue
			}
			dedupe[view.Hash] = len(out)
			out = append(out, view)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"findings": out})
	}
}

// parseProjectAndHash extracts {name} and {hash} from
// /api/projects/{name}/findings/{hash}, returning "" on shape mismatch.
func parseProjectAndHash(urlPath string) (name, hash string) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	// Expect: api, projects, {name}, findings, {hash}.
	if len(parts) != 5 || parts[0] != "api" || parts[1] != "projects" || parts[3] != "findings" {
		return "", ""
	}
	return parts[2], parts[4]
}

// unifiedDiff returns a minimal before/after rendering: every pre-image
// line prefixed with "- " and every post-image line with "+ ". It is not
// RFC-conformant; the SPA only needs a textual delta for its modal pane.
func unifiedDiff(pre, post string) string {
	if pre == post {
		return ""
	}
	var b strings.Builder
	for _, l := range strings.Split(pre, "\n") {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	for _, l := range strings.Split(post, "\n") {
		b.WriteString("+ ")
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

// FindingDetail handles GET /api/projects/{name}/findings/{hash}.
//
// Returns the FindingView for the requested hash plus an optional
// `diff_preview` block. The diff is rendered only when the caller
// supplies apply hints via query params (target_file + snippet are the
// minimum; strategy + anchor are optional). This mirrors how the SPA
// collects the apply object client-side from the list view and feeds
// it back here for the detail modal's before/after pane.
func FindingDetail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name, hash := parseProjectAndHash(r.URL.Path)
		if name == "" || hash == "" {
			http.NotFound(w, r)
			return
		}
		cfg := deps.Config()
		if cfg == nil {
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		proj, ok := findProject(cfg, name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		st, err := state.Load(cfg.Daemon.OutputRoot, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		todosPath := filepath.Join(cfg.Daemon.OutputRoot, name, "todos.md")
		latestRunHashes, allEntries, err := parseTodosLatestRun(todosPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hashLower := strings.ToLower(hash)
		// Take the most recent occurrence (file is oldest-first).
		var found *todosEntry
		for i := range allEntries {
			if allEntries[i].Hash == hashLower {
				e := allEntries[i]
				found = &e
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		view := buildFindingView(*found, st, latestRunHashes)

		// Diff preview is opt-in via query params.
		var diff string
		q := r.URL.Query()
		if q.Get("target_file") != "" && q.Get("snippet") != "" {
			pre, post, _, perr := apply.Preview(apply.ApplyRequest{
				ProjectRoot: proj.Path,
				TargetFile:  q.Get("target_file"),
				Strategy:    q.Get("strategy"),
				Anchor:      q.Get("anchor"),
				Snippet:     q.Get("snippet"),
			})
			if perr == nil {
				diff = unifiedDiff(string(pre), string(post))
			}
		}

		// Flat response shape: every FindingView field at the top level plus
		// apply_eligible + diff_preview. The SPA's modal binds to active.X
		// directly without re-aliasing.
		out := struct {
			FindingView
			ApplyEligible bool   `json:"apply_eligible"`
			DiffPreview   string `json:"diff_preview,omitempty"`
		}{
			FindingView:   view,
			ApplyEligible: apply.EligibleCategories[strings.ToLower(found.Category)],
			DiffPreview:   diff,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
