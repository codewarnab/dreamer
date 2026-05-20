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

	// Command groups for styled help output.
	root.AddGroup(commandGroups...)

	// Core commands.
	analyzeCmd := newAnalyzeCommand()
	analyzeCmd.GroupID = groupCore
	root.AddCommand(analyzeCmd)

	daemonCmd := newDaemonCommand()
	daemonCmd.GroupID = groupCore
	root.AddCommand(daemonCmd)

	startCmd := newStartCommand()
	startCmd.GroupID = groupCore
	root.AddCommand(startCmd)

	stopCmd := newStopCommand()
	stopCmd.GroupID = groupCore
	root.AddCommand(stopCmd)

	statusCmd := newStatusCommand()
	statusCmd.GroupID = groupCore
	root.AddCommand(statusCmd)

	// Setup & Config commands.
	setupCmd := newSetupCommand()
	setupCmd.GroupID = groupSetup
	root.AddCommand(setupCmd)

	addCmd := newAddCommand()
	addCmd.GroupID = groupSetup
	root.AddCommand(addCmd)

	startupCmd := newStartupCommand()
	startupCmd.GroupID = groupSetup
	root.AddCommand(startupCmd)

	// Inspect commands.
	lsCmd := newListChatsCommand()
	lsCmd.GroupID = groupInspect
	root.AddCommand(lsCmd)

	webCmd := newWebCommand()
	webCmd.GroupID = groupInspect
	root.AddCommand(webCmd)

	// Styled help output via lipgloss.
	root.SetHelpFunc(styledHelp)

	return root
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
