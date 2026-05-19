package web

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"dreamer/internal/config"
	"dreamer/internal/logging"
	"dreamer/internal/pipeline"
)

// Options bundles the dependencies a Server needs.
type Options struct {
	Config *config.Config
	Logger *logging.Logger
	Events *pipeline.EventBus
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

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; font-src 'self'")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="csrf-token" content="%s"><title>dreamer</title></head><body><div id="app"></div></body></html>`, s.csrfToken)
}

func (s *Server) attachAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}
