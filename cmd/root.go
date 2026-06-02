package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"dreamer/internal/errs"
	"dreamer/internal/skill"

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

// skillContent is the embedded agent skill playbook, loaded from the
// internal/skill package so the markdown lives alongside its Go wrapper.
var skillContent string

// configPath is the resolved --config flag value, shared by all subcommands
// via PersistentFlags on the root command.
var configPath string

// noColor is true when --no-color is passed or NO_COLOR env var is set.
// Disables all ANSI output (lipgloss styles, banners, etc.).
var noColor bool

// quiet is true when --quiet is passed. Suppresses decorative output
// (banners, navigation hints, trailing tips) for agent-friendly output.
var quiet bool

func init() {
	// NO_COLOR env var (https://no-color.org): any value = disable color.
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		noColor = true
	}

	// --no-color flag: must be checked before package-level styles are
	// created. We scan os.Args directly because Cobra hasn't parsed flags yet.
	if !noColor {
		for _, arg := range os.Args[1:] {
			if arg == "--no-color" {
				noColor = true
				break
			}
		}
	}

	if noColor {
		lipgloss.SetColorProfile(termenv.Ascii)
	}

	skillContent = skill.Playbook
}

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
			if skill, _ := cmd.Flags().GetBool("skill"); skill {
				cmd.Print(skillContent)
				return nil
			}
			return cmd.Help()
		},
	}

	root.PersistentFlags().StringVarP(&configPath, "config", "c", "", "Path to config file (default: <UserConfigDir>/dreamer/config.yaml)")
	root.PersistentFlags().BoolVar(&noColor, "no-color", false, "Disable colored output (also respects NO_COLOR env var)")
	root.PersistentFlags().BoolVarP(&quiet, "quiet", "q", false, "Suppress decorative output for machine/agent use")
	root.Flags().Bool("skill", false, "Print agent skill playbook to stdout and exit")
	root.Flags().MarkHidden("skill")

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

	removeCmd := newRemoveCommand()
	removeCmd.GroupID = groupSetup
	root.AddCommand(removeCmd)

	startupCmd := newStartupCommand()
	startupCmd.GroupID = groupSetup
	root.AddCommand(startupCmd)

	versionCmd := newVersionCommand()
	versionCmd.GroupID = groupSetup
	root.AddCommand(versionCmd)

	updateCmd := newUpdateCommand()
	updateCmd.GroupID = groupSetup
	root.AddCommand(updateCmd)

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

// ExitCode maps an error to a process exit code for agent consumption.
//
//	0 = success (no error)
//	1 = general/unexpected error
//	2 = usage/config error (bad flags, missing config, invalid path)
//	3 = provider error (not installed, unavailable, rate limited)
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *errs.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case errs.KindConfigInvalid:
			return 2
		case errs.KindNotInstalled, errs.KindRateLimit, errs.KindProviderUnavailable:
			return 3
		}
	}
	return 1
}
