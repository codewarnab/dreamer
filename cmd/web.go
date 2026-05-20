package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"dreamer/internal/config"
)

func newWebCommand() *cobra.Command {
	var (
		openFlag bool
	)
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Print the dreamer web UI URL (with --open, launch a browser).",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			if err := probeHealth(url+"/api/health", 250*time.Millisecond); err != nil {
				line1 := fmt.Sprintf("daemon UI not running on %s", url)
				line2 := "start with:"
				line3 := "dreamer daemon"
				maxLen := len(line1)
				if len(line2)+2 > maxLen {
					maxLen = len(line2) + 2
				}
				if len(line3)+4 > maxLen {
					maxLen = len(line3) + 4
				}
				w := maxLen + 4 // padding inside the box
				fmt.Fprintf(cmd.OutOrStderr(), "╔%s╗\n", strings.Repeat("═", w))
				fmt.Fprintf(cmd.OutOrStderr(), "║  %-*s  ║\n", maxLen, line1)
				fmt.Fprintf(cmd.OutOrStderr(), "╠%s╣\n", strings.Repeat("═", w))
				fmt.Fprintf(cmd.OutOrStderr(), "║  %-*s  ║\n", maxLen, line2)
				fmt.Fprintf(cmd.OutOrStderr(), "║    %-*s  ║\n", maxLen-2, line3)
				fmt.Fprintf(cmd.OutOrStderr(), "╚%s╝\n", strings.Repeat("═", w))
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), url)
			if openFlag {
				if err := openBrowser(url); err != nil {
					fmt.Fprintf(cmd.OutOrStderr(), "failed to launch browser: %v\n", err)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&openFlag, "open", false, "Open the URL in the OS default browser.")
	return cmd
}

func resolveWebPort(cfg *config.Config) (int, error) {
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
