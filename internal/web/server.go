package web

import (
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

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
	"dreamer/internal/web/handlers"
)

// Options bundles the dependencies a Server needs.
type Options struct {
	Config *config.Config
	Logger *logging.Logger
	Events *pipeline.EventBus
	// ConfigPtr, when non-nil, provides the live (atomically swappable) config
	// used by handlers that must see post-overlay-reload values. Nil-safe: when
	// unset, Server.currentConfig falls back to Options.Config.
	ConfigPtr *atomic.Pointer[config.Config]

	// OverlayPath is the absolute path to ui-overrides.yaml used by the
	// settings PUT handler. Empty disables overlay writes.
	OverlayPath string
	// Runner, when non-nil, enqueues on-demand runs for the run handler.
	Runner *Runner
	// RestartHook, when non-nil, is invoked by /api/daemon/restart to trigger
	// graceful daemon shutdown (e.g. cancel the signal context).
	RestartHook func() error
	// Activity, when non-nil, provides a snapshot of recent pipeline events
	// for the dashboard live_activity panel.
	Activity *ActivityRing
}

// Server is the embedded HTTP server lifecycle handle.
type Server struct {
	opts      Options
	csrfToken string
	httpSrv   *http.Server
	listener  net.Listener
	addr      string
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
	return &Server{opts: opts, csrfToken: MintCSRFToken()}, nil
}

// Addr returns the bound TCP address (host:port) after Start.
func (s *Server) Addr() string { return s.addr }

// CSRFToken returns the per-process CSRF token minted at construction.
func (s *Server) CSRFToken() string { return s.csrfToken }

// currentConfig returns the live config when an atomic pointer is wired in,
// otherwise the static Options.Config snapshot captured at construction.
func (s *Server) currentConfig() *config.Config {
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
	if port == 0 {
		portPath := filepath.Join(s.opts.Config.Daemon.OutputRoot, "web.port")
		_ = os.WriteFile(portPath, []byte(fmt.Sprintf("%d\n", l.Addr().(*net.TCPAddr).Port)), 0o644)
	}
	s.httpSrv = &http.Server{Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		s.opts.Logger.Info("web start", logging.Any("host", host), logging.Any("port", port), logging.Any("bind_addr", s.addr))
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
	return s.httpSrv.Shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	staticSub, _ := fs.Sub(assets, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
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
}

type projectNavItem struct{ Name string }

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.renderIndex(w, r)
}

func (s *Server) renderIndex(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	tmpl, err := template.ParseFS(assets, "templates/layout.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	items := make([]projectNavItem, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		items = append(items, projectNavItem{Name: p.Name})
	}
	data := layoutData{
		CSRFToken:         s.csrfToken,
		Projects:          items,
		OverlayParseError: cfg.Notices.OverlayParseError,
		RestartRequired:   cfg.Notices.RestartRequired,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self'; connect-src 'self'")
	_ = tmpl.Execute(w, data)
}

func (s *Server) attachAPI(mux *http.ServeMux) {
	deps := handlers.Deps{
		Config: s.currentConfig,
		Events: s.opts.Events,
		OverlayPath: func() string {
			return s.opts.OverlayPath
		},
		RecentActivity: func() []pipeline.Event {
			if s.opts.Activity == nil {
				return nil
			}
			return s.opts.Activity.Snapshot()
		},
		EnqueueRun: func(name string) (string, bool, error) {
			if s.opts.Runner == nil {
				return "", false, fmt.Errorf("runner not configured")
			}
			return s.opts.Runner.Enqueue(name)
		},
		RestartDaemon: func() error {
			if s.opts.RestartHook == nil {
				return fmt.Errorf("restart hook not configured")
			}
			return s.opts.RestartHook()
		},
	}

	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mux.Handle("/api/dashboard", handlers.Dashboard(deps))
	mux.Handle("/api/projects", handlers.ProjectsList(deps))
	mux.Handle("/api/providers", handlers.Providers(deps))
	mux.Handle("/api/settings", handlers.Settings(deps))
	mux.Handle("/api/logs/tail", handlers.LogsTail(deps))
	mux.Handle("/api/events", handlers.Events(deps))
	mux.Handle("/api/fs/exists", handlers.FSExists(deps))
	mux.Handle("/api/daemon/restart", handlers.DaemonRestart(deps))

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
			handlers.ProjectDetail(deps)(w, r)
		case len(parts) == 2 && parts[1] == "findings":
			handlers.ProjectFindings(deps)(w, r)
		case len(parts) == 2 && parts[1] == "run":
			handlers.Run(deps)(w, r)
		case len(parts) == 2 && parts[1] == "chats":
			handlers.ProjectChats(deps)(w, r)
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
