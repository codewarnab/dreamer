package cmd

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"dreamer/internal/errs"
)

// Styled error output — reuses the palette from help.go.
var (
	errLabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true) // red
	errFlagStyle  = lipgloss.NewStyle().Foreground(colorAccent)                          // mint green
	errDescStyle  = lipgloss.NewStyle().Foreground(colorDesc)                            // gray
	errHintStyle  = lipgloss.NewStyle().Foreground(colorDim)                             // dim
	errAmberStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffb86c")).Bold(true) // amber
)

// errBlock is a styled error block builder used by all error helpers in this
// file. Centralising the visual structure here means indentation, spacing,
// and color roles are defined once and propagate automatically.
type errBlock struct{ b strings.Builder }

func (e *errBlock) label(text string) { e.b.WriteString(errLabelStyle.Render("  "+text) + "\n\n") }
func (e *errBlock) amber(text string) { e.b.WriteString(errAmberStyle.Render("  "+text) + "\n\n") }
func (e *errBlock) line(text string)  { e.b.WriteString("  " + errDescStyle.Render(text) + "\n") }
func (e *errBlock) code(text string)  { e.b.WriteString("    " + errFlagStyle.Render(text) + "\n") }
func (e *errBlock) hint(text string)  { e.b.WriteString(errHintStyle.Render("  "+text) + "\n") }
func (e *errBlock) gap()              { e.b.WriteString("\n") }
func (e *errBlock) String() string    { return "\n" + e.b.String() }

// checksumDisplayLen is how many hex characters of a SHA-256 digest to show
// in the checksum-mismatch error. Full 64-char hashes are noisy in a terminal;
// 16 chars is unambiguous for human comparison while remaining readable.
const checksumDisplayLen = 16

// ── Phase 1c: missingFlagError, missingArgError, invalidFlagValueError ────────
//
// Each helper is split into a pure format* function (no I/O, testable) and an
// imperative *Error shell that prints to stderr and returns alreadyPrintedError.

// formatMissingFlag builds the styled block for a missing required flag.
func formatMissingFlag(cmd *cobra.Command, flagName, description, example string) string {
	var blk errBlock
	blk.label("Missing required flag: " + "--" + flagName)
	if description != "" {
		blk.line(description)
		blk.gap()
	}
	if example != "" {
		blk.line("Usage:")
		blk.code(example)
		blk.gap()
	}
	blk.hint(fmt.Sprintf("Run '%s --help' for all available flags.", cmd.CommandPath()))
	return blk.String()
}

// missingFlagError prints a styled error for a missing required flag and
// returns alreadyPrintedError so Execute() skips the generic "Error:" prefix.
func missingFlagError(cmd *cobra.Command, flagName, description, example string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatMissingFlag(cmd, flagName, description, example))
	return &alreadyPrintedError{fmt.Errorf("missing required flag: --%s", flagName)}
}

// formatMissingArg builds the styled block for a missing positional argument.
func formatMissingArg(cmd *cobra.Command, argName, description, example string) string {
	var blk errBlock
	blk.label("Missing required argument: " + argName)
	if description != "" {
		blk.line(description)
		blk.gap()
	}
	if example != "" {
		blk.line("Usage:")
		blk.code(example)
		blk.gap()
	}
	blk.hint(fmt.Sprintf("Run '%s --help' for all available options.", cmd.CommandPath()))
	return blk.String()
}

// missingArgError prints a styled error for a missing positional argument and
// returns alreadyPrintedError so Execute() skips the generic "Error:" prefix.
func missingArgError(cmd *cobra.Command, argName, description, example string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatMissingArg(cmd, argName, description, example))
	return &alreadyPrintedError{fmt.Errorf("missing required argument: %s", argName)}
}

// formatInvalidFlagValue builds the styled block for an invalid flag value.
func formatInvalidFlagValue(cmd *cobra.Command, flagName, got string, valid []string) string {
	var blk errBlock
	blk.label("Invalid value for --" + flagName + ": " + got)
	blk.line("Valid values: " + strings.Join(valid, ", "))
	blk.gap()
	blk.hint(fmt.Sprintf("Run '%s --help' for all available options.", cmd.CommandPath()))
	return blk.String()
}

// invalidFlagValueError prints a styled error for an invalid flag value and
// returns alreadyPrintedError so Execute() skips the generic "Error:" prefix.
func invalidFlagValueError(cmd *cobra.Command, flagName, got string, valid []string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatInvalidFlagValue(cmd, flagName, got, valid))
	return &alreadyPrintedError{fmt.Errorf("invalid value for --%s: %s", flagName, got)}
}

// ── Phase 1d: structured errs.Error renderers ─────────────────────────────────

// formatStructuredError returns a styled error block for known *errs.Error
// types, or "" if the error is not a recognized structured kind. The caller
// (Execute) is responsible for printing and returning the sentinel.
func formatStructuredError(err error) string {
	var e *errs.Error
	if !errors.As(err, &e) {
		return ""
	}
	switch e.Kind {
	case errs.KindNotInstalled:
		return formatNotInstalled(e)
	case errs.KindRateLimit:
		return formatRateLimit(e)
	case errs.KindProviderUnavailable:
		return formatProviderUnavailable(e)
	case errs.KindConfigInvalid:
		return formatConfigInvalid(e)
	}
	return ""
}

// formatNotInstalled renders a styled block for a KindNotInstalled error.
func formatNotInstalled(e *errs.Error) string {
	var blk errBlock
	blk.label("Provider not installed: " + e.Provider)
	if e.Hint != "" {
		blk.line(e.Hint)
	} else {
		blk.line("Ensure the provider binary is in your PATH and try again.")
	}
	blk.gap()
	blk.hint("Run 'dreamer setup' to reconfigure your provider.")
	return blk.String()
}

// formatRateLimit renders a styled block for a KindRateLimit error.
func formatRateLimit(e *errs.Error) string {
	var blk errBlock
	blk.amber("Rate limit reached: " + e.Provider)

	var retryAfter time.Duration
	if raw, ok := errs.Detail(e, "retry_after"); ok {
		retryAfter, _ = raw.(time.Duration)
	}
	if retryAfter > 0 {
		blk.line(fmt.Sprintf("Try again in %s.", retryAfter.Round(time.Second)))
	} else {
		blk.line("Try again shortly.")
	}
	blk.gap()
	blk.hint("If this persists, check your provider's usage quota or switch providers with --provider.")
	return blk.String()
}

// formatProviderUnavailable renders a styled block for a KindProviderUnavailable error.
func formatProviderUnavailable(e *errs.Error) string {
	var blk errBlock
	blk.label("Provider unavailable: " + e.Provider)
	if e.Cause != nil {
		blk.line(e.Cause.Error())
		blk.gap()
	}
	blk.line("Troubleshooting steps:")
	blk.code("1. Start or restart the provider process.")
	blk.code("2. Verify authentication credentials.")
	blk.code("3. Re-run 'dreamer setup' to reconfigure.")
	return blk.String()
}

// formatConfigInvalid renders a styled block for a KindConfigInvalid error.
func formatConfigInvalid(e *errs.Error) string {
	var blk errBlock
	blk.amber("Invalid configuration")

	if raw, ok := errs.Detail(e, "field"); ok {
		if field, ok := raw.(string); ok && field != "" {
			blk.line("Field: " + field)
		}
	}
	if raw, ok := errs.Detail(e, "value"); ok && raw != nil {
		blk.line(fmt.Sprintf("Value: %v", raw))
	}
	blk.gap()
	blk.line("Edit your config.yaml to fix the invalid value:")
	blk.code("dreamer setup --force  # rewrite config interactively")
	return blk.String()
}

// ── Phase 2: config-not-found and config-exists ───────────────────────────────

// formatConfigNotFound builds the styled block for a missing config file.
func formatConfigNotFound(cmd *cobra.Command, cfgPath string) string {
	var blk errBlock
	blk.label("Config file not found: " + cfgPath)
	blk.gap()
	blk.line("Dreamer hasn't been set up yet. Run the setup wizard:")
	blk.gap()
	blk.code("dreamer setup")
	blk.gap()
	blk.line("Or use --config to point to an existing file:")
	blk.gap()
	blk.code(cmd.CommandPath() + " --config /path/to/config.yaml")
	return blk.String()
}

// configNotFoundError prints a styled error for a missing config file and
// returns alreadyPrintedError so Execute() skips the generic "Error:" prefix.
func configNotFoundError(cmd *cobra.Command, cfgPath string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatConfigNotFound(cmd, cfgPath))
	return &alreadyPrintedError{fmt.Errorf("config file not found: %s", cfgPath)}
}

// formatConfigExists builds the styled block for an existing config file.
func formatConfigExists(cfgPath string) string {
	var blk errBlock
	blk.amber("Config already exists: " + cfgPath)
	blk.gap()
	blk.line("Pass --force to overwrite it:")
	blk.gap()
	blk.code("dreamer setup --force")
	return blk.String()
}

// configExistsError prints a styled error for an existing config file and
// returns alreadyPrintedError so Execute() skips the generic "Error:" prefix.
func configExistsError(cmd *cobra.Command, cfgPath string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatConfigExists(cfgPath))
	return &alreadyPrintedError{fmt.Errorf("config already exists: %s", cfgPath)}
}

// ── Phase 4a: job-not-found ───────────────────────────────────────────────────

// formatJobNotFound builds the styled block for a missing job ID.
func formatJobNotFound(jobID string) string {
	var blk errBlock
	blk.label("Job not found: " + jobID)
	blk.gap()
	blk.line("List available jobs with:")
	blk.gap()
	blk.code("dreamer jobs list")
	blk.gap()
	blk.line("Or launch the interactive dashboard:")
	blk.gap()
	blk.code("dreamer jobs")
	return blk.String()
}

// jobNotFoundError prints the styled block and returns alreadyPrintedError.
// Use this at top-level call sites (show, run, logs, runs, edit).
//
// Note: the setJobEnabled store.Update callback cannot use this helper
// because it returns from inside a closure — it keeps fmt.Errorf("job %q
// not found", jobID) and falls through to Execute()'s plain fallback.
// This is a documented exception; see alreadyPrintedError's comment.
func jobNotFoundError(cmd *cobra.Command, jobID string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatJobNotFound(jobID))
	return &alreadyPrintedError{fmt.Errorf("job not found: %s", jobID)}
}

// ── Phase 4c: provider-not-background-safe ────────────────────────────────────

// formatProviderNotBackgroundSafe builds the styled block for a provider that
// cannot run as a background job.
func formatProviderNotBackgroundSafe(cmd *cobra.Command, providerID string) string {
	var blk errBlock
	blk.label("Provider not safe for background execution: " + providerID)
	blk.gap()
	blk.line("This provider requires an interactive terminal and cannot run")
	blk.line("as a background job via the OS scheduler.")
	blk.gap()
	blk.line("Use the interactive wizard to pick a background-safe provider:")
	blk.gap()
	blk.code(cmd.CommandPath() + " --interactive")
	blk.gap()
	blk.hint("Run 'dreamer jobs create --help' to see which providers support background execution.")
	return blk.String()
}

// providerNotBackgroundSafeError prints a styled error for a provider that
// cannot run as a background job and returns alreadyPrintedError.
func providerNotBackgroundSafeError(cmd *cobra.Command, providerID string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatProviderNotBackgroundSafe(cmd, providerID))
	return &alreadyPrintedError{fmt.Errorf("provider not background-safe: %s", providerID)}
}

// ── Phase 5a: job conflict ────────────────────────────────────────────────────

// formatConflict builds the styled block for an in-progress analysis conflict.
func formatConflict(cmd *cobra.Command, projectName, jobID, status string) string {
	var blk errBlock
	blk.amber("Analysis already in progress")
	blk.gap()
	blk.line("Project:  " + projectName)
	blk.line("Job:      " + jobID)
	blk.line("Status:   " + status)
	blk.gap()
	blk.line("Pass --force to bypass this check:")
	blk.gap()
	blk.code(cmd.CommandPath() + " --force")
	return blk.String()
}

// conflictError prints a styled error for an in-progress analysis conflict
// and returns alreadyPrintedError.
func conflictError(cmd *cobra.Command, projectName, jobID, status string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatConflict(cmd, projectName, jobID, status))
	return &alreadyPrintedError{fmt.Errorf("analysis in progress: %s (job %s)", projectName, jobID)}
}

// ── Phase 6a: update network error ───────────────────────────────────────────

// formatUpdateNetworkError builds the styled block for an update network failure.
func formatUpdateNetworkError(cause error) string {
	var blk errBlock
	blk.label("Update check failed")
	blk.gap()
	blk.line("Could not reach GitHub. Check your internet connection and try again.")
	blk.line("If the problem persists, download manually from:")
	blk.gap()
	blk.code("https://github.com/" + updateRepo + "/releases")
	blk.gap()
	blk.hint(fmt.Sprintf("Underlying error: %v", cause))
	return blk.String()
}

// updateNetworkError prints a styled error for a failed update network request
// and returns alreadyPrintedError.
func updateNetworkError(cmd *cobra.Command, cause error) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatUpdateNetworkError(cause))
	return &alreadyPrintedError{fmt.Errorf("update network error: %w", cause)}
}

// ── Phase 6b: update asset not found ─────────────────────────────────────────

// formatUpdateAssetNotFound builds the styled block for a missing release asset.
func formatUpdateAssetNotFound(assetName string) string {
	var blk errBlock
	blk.label("No release asset found for your platform")
	blk.gap()
	blk.line(fmt.Sprintf("Detected: %s/%s (%s)", runtime.GOOS, runtime.GOARCH, assetName))
	blk.line("Check available assets at:")
	blk.gap()
	blk.code("https://github.com/" + updateRepo + "/releases")
	blk.gap()
	blk.line("If your platform is not listed, build from source:")
	blk.gap()
	blk.code(`go build -trimpath -ldflags="-s -w" -o dreamer .`)
	return blk.String()
}

// updateAssetNotFoundError prints a styled error for a missing release asset
// and returns alreadyPrintedError.
func updateAssetNotFoundError(cmd *cobra.Command, assetName string) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatUpdateAssetNotFound(assetName))
	return &alreadyPrintedError{fmt.Errorf("no release asset: %s", assetName)}
}

// ── Phase 6c: checksum mismatch ───────────────────────────────────────────────

// formatChecksumMismatch builds the styled block for a checksum verification failure.
func formatChecksumMismatch(expected, got string) string {
	var blk errBlock
	blk.label("⚠ Security warning: checksum mismatch")
	blk.gap()
	blk.line("The downloaded binary does not match the expected checksum.")
	blk.line("It may be corrupted or tampered with — do not use it.")
	blk.gap()
	if len(expected) >= checksumDisplayLen {
		blk.line(fmt.Sprintf("Expected: %s...", expected[:checksumDisplayLen]))
	} else {
		blk.line("Expected: " + expected)
	}
	if len(got) >= checksumDisplayLen {
		blk.line(fmt.Sprintf("Got:      %s...", got[:checksumDisplayLen]))
	} else {
		blk.line("Got:      " + got)
	}
	blk.gap()
	blk.line("Download manually from a trusted source:")
	blk.gap()
	blk.code("https://github.com/" + updateRepo + "/releases")
	return blk.String()
}

// checksumMismatchError prints a styled security warning for a checksum mismatch.
// It writes directly to os.Stderr because verifySHA256 is called deep inside
// downloadAndReplace where no *cobra.Command is available.
func checksumMismatchError(expected, got string) error {
	fmt.Fprint(os.Stderr, formatChecksumMismatch(expected, got))
	return &alreadyPrintedError{fmt.Errorf("checksum mismatch")}
}

// ── Phase 7: filesystem and port-in-use ──────────────────────────────────────

// formatOutputDirError builds the styled block for an output directory creation failure.
func formatOutputDirError(path string, cause error) string {
	var blk errBlock
	if os.IsPermission(cause) {
		blk.label("Cannot create output directory: " + path)
		blk.gap()
		blk.line("Permission denied. Check that the path is writable,")
		blk.line("or set a different output directory in your config:")
		blk.gap()
		blk.code("daemon:")
		blk.code("  output_root: /home/youruser/.dreamer/output")
	} else {
		blk.label("Cannot create output directory: " + path)
		blk.gap()
		blk.hint(fmt.Sprintf("Underlying error: %v", cause))
	}
	return blk.String()
}

// outputDirError prints a styled error for an output directory failure
// and returns alreadyPrintedError.
func outputDirError(cmd *cobra.Command, path string, cause error) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatOutputDirError(path, cause))
	return &alreadyPrintedError{fmt.Errorf("create output dir %q: %w", path, cause)}
}

// formatPortInUse builds the styled block for a port-already-in-use failure.
func formatPortInUse(port int, cause error) string {
	var blk errBlock
	blk.label(fmt.Sprintf("Port %d is already in use", port))
	blk.gap()
	blk.line("Another standalone server or daemon may be active on this port.")
	blk.line("Use 'dreamer web' (no --serve) to discover and open the active server,")
	blk.line("or bind to an ephemeral port:")
	blk.gap()
	blk.code("dreamer web --serve --port 0")
	blk.gap()
	blk.hint(fmt.Sprintf("Underlying error: %v", cause))
	return blk.String()
}

// portInUseError prints a styled error for a port-in-use failure
// and returns alreadyPrintedError.
func portInUseError(cmd *cobra.Command, port int, cause error) error {
	fmt.Fprint(cmd.ErrOrStderr(), formatPortInUse(port, cause))
	return &alreadyPrintedError{fmt.Errorf("port %d already in use: %w", port, cause)}
}

// ── Utility ───────────────────────────────────────────────────────────────────

// sortedKeys returns a sorted slice of string keys from a map[string]bool.
// Used to produce deterministic valid-values lists from set maps.
func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
