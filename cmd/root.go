package cmd

import "github.com/spf13/cobra"

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

	root.AddCommand(newAnalyzeCommand())
	root.AddCommand(newDaemonCommand())
	root.AddCommand(newConfigCommand())
	root.AddCommand(newListChatsCommand())

	return root
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
