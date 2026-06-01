package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Build-time variables, overridden via -ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Version returns the build version string injected via -ldflags.
// Returns "dev" for unbuilt/local runs.
func Version() string { return version }

func newVersionCommand() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if verbose {
				fmt.Fprintf(cmd.OutOrStdout(), "dreamer %s\n", version)
				fmt.Fprintf(cmd.OutOrStdout(), "  commit:    %s\n", commit)
				fmt.Fprintf(cmd.OutOrStdout(), "  built:     %s\n", date)
				fmt.Fprintf(cmd.OutOrStdout(), "  go:        %s\n", runtime.Version())
				fmt.Fprintf(cmd.OutOrStdout(), "  os/arch:   %s/%s\n", runtime.GOOS, runtime.GOARCH)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "dreamer %s\n", version)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed version information")
	return cmd
}
