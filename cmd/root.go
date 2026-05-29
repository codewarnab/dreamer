package cmd

import (
	"errors"
	"fmt"

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
		SilenceUsage:  true,
		SilenceErrors: true, // We print styled errors ourselves.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: <UserConfigDir>/dreamer/config.yaml)")

	// Suggest corrections for typos in commands and flags.
	root.SetFlagErrorFunc(styledFlagError)

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

	jobsCmd := newJobsCommand()
	jobsCmd.GroupID = groupCore
	root.AddCommand(jobsCmd)

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

	// Internal commands (hidden — spawned by the analyzer pipeline).
	root.AddCommand(newMCPServerCommand())
	root.AddCommand(newRecordFindingCommand())

	// Styled help output via lipgloss.
	root.SetHelpFunc(styledHelp)

	return root
}

// Execute runs the root command. When Cobra reports an unknown command, it
// prints a styled "did you mean?" suggestion. All other errors are printed
// to stderr as "Error: <msg>".
func Execute() error {
	err := rootCmd.Execute()
	if err == nil {
		return nil
	}
	var already *alreadyPrintedError
	if errors.As(err, &already) {
		return err // styled output already printed.
	}
	if suggestFromError(err) {
		return err
	}
	fmt.Fprintf(rootCmd.ErrOrStderr(), "Error: %v\n", err)
	return err
}
