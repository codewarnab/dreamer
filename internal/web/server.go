package web

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
	"dreamer/internal/web/handlers"
)

// SSE and HTTP server tuning.
const (
	// sseSubscribeBuffer is the channel buffer size for SSE event
	// subscribers. Small buffer to avoid lag; slow consumers drop.
	sseSubscribeBuffer = 16

	// readHeaderTimeout is the http.Server ReadHeaderTimeout. Prevents
	// slowloris-style attacks on the loopback server.
	readHeaderTimeout = 5 * time.Second
)

// Options bundles the dependencies a Server needs.
type Options struct {
	Config *config.App
	Logger *logging.Logger
	Events *pipeline.EventBus
	// ConfigPtr, when non-nil, provides the live (atomically swappable) config
	// used by handlers that must see post-overlay-reload values. Nil-safe: when
	// unset, Server.currentConfig falls back to Options.Config.
	ConfigPtr *atomic.Pointer[config.App]

	// OverlayPath is the absolute path to ui-overrides.yaml used by the
	// settings PUT handler. Empty disables overlay writes.
	OverlayPath string
	// ConfigPath is the absolute path to config.yaml. Used by the project
	// delete handler to rewrite the base config (comment-preserving). Empty
	// disables web-side project removal (e.g. standalone read-only mode).
	ConfigPath string
	// EnqueueRun, when non-nil, enqueues on-demand runs for the run handler.
	// Returns (jobID, true, nil) on success; ("", false, nil) when a job is
	// already active for the project (queue-level dedup).
	EnqueueRun func(projectName string) (jobID string, accepted bool, err error)
	// RestartHook, when non-nil, is invoked by /api/daemon/restart to trigger
	// graceful daemon shutdown (e.g. cancel the signal context).
	RestartHook func() error
	// ShutdownCtx is cancelled when the daemon is shutting down.
	// Used by background goroutines (e.g. async job runs) for graceful cancellation.
	ShutdownCtx context.Context
	// Activity, when non-nil, provides a snapshot of recent pipeline events
	// for the dashboard live_activity panel.
	Activity *ActivityRing
	// StateCache, when non-nil, is shared with handlers for read-through
	// caching of state.json and history.json. Nil creates a fresh cache.
	StateCache *state.StateCache
	// Jobs holds background job dependencies. When zero-valued, job
	// endpoints return 503.
	Jobs handlers.JobDeps
	// Standalone suppresses the internal "web start" log line so the
	// CLI wrapper can print its own user-facing startup banner without
	// duplication.
	Standalone bool
	// DevDir, when non-empty, is the absolute path to the internal/web
	// source directory. Templates and static files are served directly from
	// disk instead of the embedded FS, so HTML/CSS/JS edits take effect on
	// the next browser refresh without a rebuild.
	DevDir string
}

// Server is the embedded HTTP server lifecycle handle.
type Server struct {
	opts      Options
	csrfToken string
	httpSrv   *http.Server
	listener  net.Listener
	addr      string
	// templates caches parsed HTML templates keyed by page name.
	// In dev mode (opts.DevDir != "") templates are re-parsed per request
	// so edits are reflected immediately. In production they are parsed
	// once at startup and never mutated.
	templates map[string]*template.Template
}

// NewServer constructs a Server but does not bind.
func NewServer(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("Options.Config required")
	}
	if opts.Logger == nil {
		return nil, fmt.Errorf("Options.Logger required")
	}
	if opts.Events == nil {
		return nil, fmt.Errorf("Options.Events required")
	}
	s := &Server{opts: opts, csrfToken: MintCSRFToken()}
	s.initTemplates()
	return s, nil
}

// initTemplates parses all page templates once at startup and caches
// them so renderPage never re-parses per request.
// In dev mode (opts.DevDir != "") this is a no-op; templates are parsed
// from disk on every request instead.
func (s *Server) initTemplates() {
	if s.opts.DevDir != "" {
		s.templates = nil // dev mode: parse on demand from disk
		return
	}
	s.templates = make(map[string]*template.Template)
	for _, page := range []string{
		"dashboard",
		"jobs",
		"job_detail",
		"settings",
		"logs",
		"providers",
		"project_overview",
		"project_findings",
		"project_chats",
		"project_history",
	} {
		tmpl, err := template.ParseFS(assets,
			"templates/layout.html",
			"templates/"+page+".html",
		)
		if err != nil {
			panic("web: parse templates/" + page + ": " + err.Error())
		}
		s.templates[page] = tmpl
	}
}

// templateFor returns the parsed template for the given page name.
// In production it is fetched from the pre-built cache.
// In dev mode it is re-parsed from disk on every call so that edits to
// HTML files are visible on the next browser refresh without a rebuild.
func (s *Server) templateFor(page string) (*template.Template, error) {
	if s.opts.DevDir != "" {
		layoutPath := filepath.Join(s.opts.DevDir, "templates", "layout.html")
		pagePath := filepath.Join(s.opts.DevDir, "templates", page+".html")
		return template.ParseFiles(layoutPath, pagePath)
	}
	tmpl, ok := s.templates[page]
	if !ok {
		return nil, fmt.Errorf("unknown page template: %s", page)
	}
	return tmpl, nil
}

// Addr returns the bound TCP address (host:port) after Start.
func (s *Server) Addr() string { return s.addr }

// CSRFToken returns the per-process CSRF token minted at construction.
func (s *Server) CSRFToken() string { return s.csrfToken }

// currentConfig returns the live config when an atomic pointer is wired in,
// otherwise the static Options.Config snapshot captured at construction.
func (s *Server) currentConfig() *config.App {
	if s.opts.ConfigPtr != nil {
		if cfg := s.opts.ConfigPtr.Load(); cfg != nil {
			return cfg
		}
	}
	return s.opts.Config
}

// Start binds the listener and serves in a background goroutine. When the
// configured port is 0, the bound ephemeral port is written to
// <output_root>/web.port so 'dreamer web' can discover it.
func (s *Server) Start() error {
	host := s.opts.Config.Web.Host
	port := s.opts.Config.Web.Port
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("bind web %s: %w", addr, err)
	}
	s.listener = l
	s.addr = l.Addr().String()
	if port == 0 && !s.opts.Standalone {
		// NOTE: Standalone mode serves in the foreground and prints its URL directly to stdout,
		// so port-file discovery is not needed. Skip writing the web.port file to prevent
		// clobbering any running daemon's ephemeral discovery port marker.
		portPath := filepath.Join(s.opts.Config.Daemon.OutputRoot, "web.port")
		portBytes := []byte(fmt.Sprintf("%d\n", l.Addr().(*net.TCPAddr).Port))
		// Atomic write (temp + rename) matches the project-wide convention
		// from B3 so a racing `dreamer web` can never observe a partial
		// or empty port file mid-write.
		// Written with SecretPerms (owner read-write only) since the port is exposed without authentication
		// and we want to prevent local unauthenticated access disclosure to other users on shared hosts.
		if err := fsutil.WriteFileAtomic(portPath, portBytes, fsutil.SecretPerms); err != nil {
			s.opts.Logger.Error("write port file failed", append([]logging.Attr{logging.Any("path", portPath)}, logging.ErrAttr(err)...)...)
		}
	}
	s.httpSrv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: readHeaderTimeout}
	go func() {
		if !s.opts.Standalone {
			s.opts.Logger.Info("web start", logging.Any("host", host), logging.Any("port", port), logging.Any("bind_addr", s.addr))
		}
		if err := s.httpSrv.Serve(l); err != nil && err != http.ErrServerClosed {
			s.opts.Logger.Error("web server error", logging.Any("err", err))
		}
	}()
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpSrv == nil {
		return nil
	}
	s.opts.Logger.Info("web stop")

	// Best-effort cleanup of the ephemeral web.port file on shutdown to avoid leaving stale port discovery files on disk.
	if s.opts.Config.Web.Port == 0 && !s.opts.Standalone {
		portPath := filepath.Join(s.opts.Config.Daemon.OutputRoot, "web.port")
		_ = os.Remove(portPath)
	}

	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	if s.opts.DevDir != "" {
		// Dev mode: serve static files directly from disk so CSS/JS edits
		// are visible on browser refresh without a rebuild.
		staticDir := filepath.Join(s.opts.DevDir, "static")
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	} else {
		staticSub, err := fs.Sub(assets, "static")
		if err != nil {
			panic("embed: static subtree missing: " + err.Error())
		}
		mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	}
	mux.HandleFunc("/", s.handleIndex)
	s.attachAPI(mux)
	return CSRFMiddleware(s.csrfToken, mux)
}

// layoutData is the template payload for the SPA shell.
type layoutData struct {
	CSRFToken         string
	Projects          []projectNavItem
	OverlayParseError string
	RestartRequired   []string
	// Extra carries page-specific data (e.g. ProjectName for project tabs).
	Extra any
}

type projectNavItem struct{ Name string }

// pageRoute maps a top-level SPA path to the page template that fills the
// layout's content block.
type pageRoute struct {
	template    string
	nameInTitle string
}

var pageRoutes = map[string]pageRoute{
	"/":          {template: "dashboard.html", nameInTitle: "dashboard"},
	"/jobs":      {template: "jobs.html", nameInTitle: "jobs"},
	"/settings":  {template: "settings.html", nameInTitle: "settings"},
	"/logs":      {template: "logs.html", nameInTitle: "logs"},
	"/providers": {template: "providers.html", nameInTitle: "providers"},
}

var projectTabTemplates = map[string]string{
	"":         "project_overview.html",
	"findings": "project_findings.html",
	"chats":    "project_chats.html",
	"history":  "project_history.html",
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/projects/") {
		s.renderProjectPage(w, r)
		return
	}
	if strings.HasPrefix(path, "/jobs/") {
		s.renderJobDetailPage(w, r)
		return
	}
	route, ok := pageRoutes[path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, r, route.template, nil)
}

func (s *Server) layoutData(extra any) layoutData {
	cfg := s.currentConfig()
	items := make([]projectNavItem, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		items = append(items, projectNavItem{Name: p.Name})
	}
	return layoutData{
		CSRFToken:         s.csrfToken,
		Projects:          items,
		OverlayParseError: cfg.Notices.OverlayParseError,
		RestartRequired:   cfg.Notices.RestartRequired,
		Extra:             extra,
	}
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, pageTemplate string, extra any) {
	// Normalize: "dashboard.html" → "dashboard", "project_overview.html" → "project_overview".
	dir := strings.TrimSuffix(pageTemplate, ".html")
	tmpl, err := s.templateFor(dir)
	if err != nil {
		http.Error(w, "unknown page template: "+pageTemplate, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// CSP allows inline <script> for per-page Alpine factory definitions
	// and 'unsafe-eval' for Alpine's Function()-constructor expression
	// evaluator (x-text, x-show, ternaries, ?? operators). Loopback-only
	// binding + CSRF + zero third-party scripts make this acceptable for
	// v1.5; future migration to Alpine's CSP build (or a single static
	// bundle) can tighten both directives.
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; font-src 'self'; connect-src 'self'")
	// Pre-render to a buffer so a mid-template error doesn't write
	// truncated HTML to the client.
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, s.layoutData(extra)); err != nil {
		s.opts.Logger.Error("template execute failed",
			logging.String("page", dir),
			logging.Any("err", err))
		http.Error(w, "template render failed", http.StatusInternalServerError)
		return
	}
	buf.WriteTo(w)
}

// renderProjectPage parses /projects/{name}[/{tab}], validates the project
// against the live config, and renders the matching tab template.
func (s *Server) renderProjectPage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/projects/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	name := parts[0]
	tab := ""
	if len(parts) == 2 {
		tab = parts[1]
	}
	tmpl, ok := projectTabTemplates[tab]
	if !ok {
		http.NotFound(w, r)
		return
	}
	cfg := s.currentConfig()
	found := false
	for _, p := range cfg.Projects {
		if p.Name == name {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, r, tmpl, struct{ ProjectName string }{ProjectName: name})
}

// renderJobDetailPage extracts the job ID from /jobs/{id}, validates it,
// and renders the job_detail template with the ID as Extra.
func (s *Server) renderJobDetailPage(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/jobs/")
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(rest, "/", 2)
	jobID := parts[0]
	if err := backgroundjobs.ValidateJobID(jobID); err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}
	s.renderPage(w, r, "job_detail.html", struct{ JobID string }{JobID: jobID})
}

func (s *Server) attachAPI(mux *http.ServeMux) {
	deps := handlers.Deps{
		Config:      s.currentConfig,
		Events:      s.opts.Events,
		Logger:      s.opts.Logger,
		ShutdownCtx: s.opts.ShutdownCtx,
		Jobs:        s.opts.Jobs,
		OverlayPath: func() string {
			return s.opts.OverlayPath
		},
		ConfigPath: func() string {
			return s.opts.ConfigPath
		},
		RecentActivity: func() []pipeline.Event {
			if s.opts.Activity == nil {
				return nil
			}
			return s.opts.Activity.Snapshot()
		},
		EnqueueRun: func(name string) (string, bool, error) {
			if s.opts.EnqueueRun == nil {
				return "", false, fmt.Errorf("runner not configured")
			}
			return s.opts.EnqueueRun(name)
		},
		RestartDaemon: func() error {
			if s.opts.RestartHook == nil {
				return fmt.Errorf("restart hook not configured")
			}
			return s.opts.RestartHook()
		},
		StateLock: handlers.NewProjectLock(),
	}
	if s.opts.StateCache != nil {
		deps.StateCache = s.opts.StateCache
	} else {
		deps.StateCache = state.NewStateCache()
	}

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mux.Handle("/api/dashboard", handlers.Dashboard(deps))
	// Route projects endpoint to handlers.Projects which manages both GET (listing projects)
	// and POST (creating/adding a new project folder) requests.
	mux.Handle("/api/projects", handlers.Projects(deps))
	mux.Handle("/api/providers", handlers.Providers(deps))
	mux.Handle("/api/settings", handlers.Settings(deps))
	mux.Handle("/api/logs/tail", handlers.LogsTail(deps))
	mux.Handle("/api/events", handlers.Events(deps))
	mux.Handle("/api/fs/exists", handlers.FSExists(deps))
	mux.Handle("/api/fs/pick-directory", handlers.FSPickDirectory(deps))
	mux.Handle("/api/daemon/restart", handlers.DaemonRestart(deps))

	// /api/jobs[/...] — background jobs endpoints.
	// RouteJobs handles all sub-routes including collection, preview, health.
	mux.Handle("/api/jobs", handlers.RouteJobs(deps))
	mux.Handle("/api/jobs/", handlers.RouteJobs(deps))

	// /api/projects/{name}[/sub...] — dispatcher routes by path shape.
	mux.HandleFunc("/api/projects/", s.routeProject(deps))
}

// routeProject parses /api/projects/{name}[/sub[/sub2]] and dispatches to
// the matching handler. Handlers re-parse the URL themselves, so we only
// need to select the right one based on path shape.
func (s *Server) routeProject(deps handlers.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		if rest == "" || rest == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		rest = strings.TrimSuffix(rest, "/")
		parts := strings.Split(rest, "/")
		switch {
		case len(parts) == 1:
			if r.Method == http.MethodDelete {
				handlers.ProjectDelete(deps)(w, r)
				return
			}
			handlers.ProjectDetail(deps)(w, r)
		case len(parts) == 2 && parts[1] == "findings":
			handlers.ProjectFindings(deps)(w, r)
		case len(parts) == 2 && parts[1] == "run":
			handlers.Run(deps)(w, r)
		case len(parts) == 2 && parts[1] == "chats":
			handlers.ProjectChats(deps)(w, r)
		case len(parts) == 2 && parts[1] == "chats:bulk-delete":
			handlers.ProjectChatsBulkDelete(deps)(w, r)
		case len(parts) == 2 && parts[1] == "history":
			handlers.ProjectHistory(deps)(w, r)
		case len(parts) == 3 && parts[1] == "findings":
			handlers.FindingDetail(deps)(w, r)
		case len(parts) == 4 && parts[1] == "findings":
			switch parts[3] {
			case "apply":
				handlers.Apply(deps)(w, r)
			case "undo":
				handlers.Undo(deps)(w, r)
			case "dismiss":
				handlers.Dismiss(deps)(w, r)
			case "resolve":
				handlers.Resolve(deps)(w, r)
			case "undismiss":
				handlers.Undismiss(deps)(w, r)
			case "unresolve":
				handlers.Unresolve(deps)(w, r)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}
}
