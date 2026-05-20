package cmd

import (
	"github.com/spf13/cobra"

	// Register provider implementations.
	_ "dreamer/internal/analyzer/providers/claudeacp"
	_ "dreamer/internal/analyzer/providers/claudecli"
	_ "dreamer/internal/analyzer/providers/codebuffsdk"
	_ "dreamer/internal/analyzer/providers/codexacp"
	_ "dreamer/internal/analyzer/providers/codexcli"
	_ "dreamer/internal/analyzer/providers/copilotacp"
	_ "dreamer/internal/analyzer/providers/copilotsdk"
	_ "dreamer/internal/analyzer/providers/geminiacp"
	_ "dreamer/internal/analyzer/providers/geminicli"
	_ "dreamer/internal/analyzer/providers/kiroacp"
	_ "dreamer/internal/analyzer/providers/openclaudecli"
	_ "dreamer/internal/analyzer/providers/opencodeacp"
	_ "dreamer/internal/analyzer/providers/opencodehttp"
)

// configPath is the resolved --config flag value, shared by all subcommands
// via PersistentFlags on the root command.
var configPath string

var rootCmd = newRootCommand()

func newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "dreamer",
		Short: "Analyze chats and generate actionable todos.",
		Long: "dreamer is a command-line tool for discovering chat history, analyzing recurring " +
			"engineering patterns, and generating actionable project todos.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: <UserConfigDir>/dreamer/config.yaml)")

	root.AddCommand(newAnalyzeCommand())
	root.AddCommand(newDaemonCommand())
	root.AddCommand(newListChatsCommand())
	root.AddCommand(newStartCommand())
	root.AddCommand(newStartupCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newStopCommand())
	root.AddCommand(newSetupCommand())
	root.AddCommand(newWebCommand())
	root.AddCommand(newAddCommand())

	return root
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
