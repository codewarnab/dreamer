package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/config"
	"dreamer/internal/pipeline"
	"dreamer/internal/state"
	"dreamer/internal/web"
)

func newWebCommand() *cobra.Command {
	var (
		openFlag   bool
		serveFlag  bool
		portFlag   int
		devFlag    bool
		devDirFlag string
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Open the dreamer web UI. With --serve, run a standalone server; otherwise print a running daemon's URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if serveFlag {
				// -1 indicates the --port flag was not specified; see serveWeb implementation
				portOverride := -1
				if cmd.Flags().Changed("port") {
					portOverride = portFlag
				}
				var devDir string
				if devFlag {
					if cmd.Flags().Changed("dev-dir") {
						devDir = devDirFlag
					} else {
						// Auto-detect internal/web/ relative to cwd.
						if _, err := os.Stat("internal/web/embed.go"); err == nil {
							devDir = "internal/web"
						} else {
							return fmt.Errorf("--dev: cannot find internal/web/ in current directory; use --dev-dir to specify the path")
						}
					}
					if abs, err := filepath.Abs(devDir); err == nil {
						devDir = abs
					}
				}
				return serveWeb(cmd, portOverride, openFlag, devDir)
			}
			return discoverWebURL(cmd, openFlag)
		},
	}
	cmd.Flags().BoolVar(&openFlag, "open", false, "Open the URL in the OS default browser.")
	cmd.Flags().BoolVar(&serveFlag, "serve", false, "Run a standalone, read-only web server in the foreground (no daemon required). NOTE: Binds loopback-only with no auth (same trust model as daemon).")
	cmd.Flags().IntVar(&portFlag, "port", 0, "With --serve, override the bind port. 0 picks an ephemeral port.")
	cmd.Flags().BoolVar(&devFlag, "dev", false, "With --serve, enable live-reload: templates and static files served from disk.")
	cmd.Flags().StringVar(&devDirFlag, "dev-dir", "", "With --dev, override the path to internal/web/. Auto-detected from cwd if omitted.")
	return cmd
}

// discoverWebURL prints the URL of a web server exposed by a running daemon,
// or a hint to start one when none is reachable. This is the default behavior
// of `dreamer web` (no --serve).
func discoverWebURL(cmd *cobra.Command, openFlag bool) error {
	if cmd.Flags().Changed("port") {
		fmt.Fprintln(cmd.OutOrStderr(), "warn: --port has no effect when running without --serve")
	}
	resolved, err := resolveConfigPath(configPath)
	if err != nil {
		return err
	}
	overlayPath, _ := config.GlobalOverlayPath()
	cfg, err := config.LoadConfigWithOverlay(resolved, overlayPath)
	if err != nil {
		return fmt.Errorf("load config %q: %w", resolved, err)
	}
	port, err := resolveWebPort(cfg)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := probeHealth(url+"/api/health", healthProbeTimeout); err != nil {
		printBox(cmd.OutOrStderr(), []string{
			fmt.Sprintf("daemon UI not running on %s", url),
			"start with:",
			"  dreamer daemon",
			"or run a standalone server:",
			"  dreamer web --serve",
		})
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), url)
	if openFlag {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "failed to launch browser: %v\n", err)
		}
	}
	return nil
}

// serveWeb runs a standalone web server in the foreground until interrupted.
// It reuses the daemon's config+logger bootstrap and signal set, but wires
// none of the producer hooks (EnqueueRun, RestartHook, Activity, Jobs,
// OverlayPath): the server is read-only, so the run/restart/jobs/settings-write
// endpoints return 503 by design.
//
// NOTE: Standalone mode (dreamer web --serve) is historically documented as "read-only" because
// it does not configure producer hooks (EnqueueRun, RestartHook, Activity, Jobs, OverlayPath).
// However, it is NOT fully read-only: endpoints that mutate finding lifecycle state
// (Apply, Undo, Dismiss, Resolve, Undismiss, Unresolve) and chat history (chats deletion and
// bulk-deletion) remain active and write to disk using deps.Config() and deps.StateCache.
// Similarly, project deletion is supported because ConfigPath is wired. This is intentional:
// standalone mode is not going to stay read-only in future versions, and these mutating
// features are kept active by design.
//
// NOTE: Standalone mode binds to the host specified in `web.host` which is strictly
// validated to be a loopback address (127.0.0.1, ::1, or localhost) by config.LoadConfig.
// This prevents unauthenticated remote LAN access to chat logs or findings.
//
// portOverride < 0 means "no --port flag";
// any value >= 0 (including 0 for an ephemeral port) overrides cfg.Web.Port.
// devDir, when non-empty, enables dev mode: templates and static files are
// served from disk so edits are visible on browser refresh without a rebuild.
func serveWeb(cmd *cobra.Command, portOverride int, openFlag bool, devDir string) error {
	resolved, err := resolveConfigPath(configPath)
	if err != nil {
		return err
	}
	overlayPath, _ := config.GlobalOverlayPath()
	cfg, logger, err := loadConfigAndLogger(resolved, overlayPath)
	if err != nil {
		return err
	}
	defer func() { _ = logger.Close() }()

	// Warn if web.enabled is explicitly false but we are serving standalone
	if cfg.Web.Enabled != nil && !*cfg.Web.Enabled {
		fmt.Fprintln(cmd.OutOrStdout(), "info: web.enabled is false in config; starting standalone server anyway as --serve was explicitly requested")
	}

	// Server.Start reads cfg.Web.Port directly, so apply the override before
	// constructing the server rather than through the live-config pointer.
	// Mutating cfg in-place is completely safe here because loadConfigAndLogger returns
	// a fresh config instance unique to this standalone serveWeb process.
	if portOverride >= 0 {
		cfg.Web.Port = portOverride
	}

	ctx, stop := signal.NotifyContext(commandContext(cmd), daemonSignals()...)
	defer stop()

	var live atomic.Pointer[config.App]
	live.Store(cfg)

	events := pipeline.NewEventBus()
	startConfigWatcher(ctx, logger, events, &live, resolved, overlayPath)

	srv, err := web.NewServer(web.Options{
		Config:      cfg,
		ConfigPtr:   &live,
		Logger:      logger,
		Events:      events,
		ShutdownCtx: ctx,
		StateCache:  state.NewStateCache(),
		Standalone:  true,
		DevDir:      devDir,
		ConfigPath:  resolved,
	})
	if err != nil {
		return fmt.Errorf("construct web server: %w", err)
	}
	if err := srv.Start(); err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("start web server failed: port %d is already in use.\n"+
				"Hint: Another standalone server or daemon may be active on this port.\n"+
				"Use 'dreamer web' (no --serve) to discover and open the active server, or run with '--port 0' to bind to an ephemeral port.\nOriginal error: %w", cfg.Web.Port, err)
		}
		return fmt.Errorf("start web server: %w", err)
	}

	url := "http://" + srv.Addr()
	mode := "read-only"
	if devDir != "" {
		mode = "dev (live-reload from " + devDir + ")"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "dreamer web listening on %s (%s)\n", url, mode)
	if openFlag {
		if err := openBrowser(url); err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "  failed to launch browser: %v\n", err)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "  opening browser...")
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), "press Ctrl+C to stop")

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), webShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown web server: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "dreamer web stopped")
	return nil
}

func resolveWebPort(cfg *config.App) (int, error) {
	outputRoot := strings.TrimSpace(cfg.Daemon.OutputRoot)
	if outputRoot != "" {
		path := filepath.Join(outputRoot, "web.port")
		if data, err := os.ReadFile(path); err == nil {
			line := strings.TrimSpace(string(data))
			if p, err := strconv.Atoi(line); err == nil && p > 0 {
				return p, nil
			}
		}
	}
	if cfg.Web.Port > 0 {
		return cfg.Web.Port, nil
	}
	return config.DefaultWebPort, nil
}

func probeHealth(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned status %d", resp.StatusCode)
	}
	return nil
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}

func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == 10048 {
		return true
	}
	return false
}
