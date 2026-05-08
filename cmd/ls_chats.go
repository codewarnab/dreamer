package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"dreamer/internal/chat"
	"github.com/spf13/cobra"
)

func newListChatsCommand() *cobra.Command {
	var projectPath string

	command := &cobra.Command{
		Use:   "ls-chats",
		Short: "List discovered chat sources.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedProjectPath, err := resolveProjectPath(projectPath)
			if err != nil {
				return err
			}

			sources, err := chat.DiscoverChats(resolvedProjectPath)
			if err != nil {
				return fmt.Errorf("discover chats for %q: %w", resolvedProjectPath, err)
			}
			if len(sources) == 0 {
				return fmt.Errorf("no chat sources discovered for %q", resolvedProjectPath)
			}

			cmd.Println("TOOL\tMODIFIED_AT\tPATH")
			for _, source := range sources {
				cmd.Printf("%s\t%s\t%s\n", source.Tool, source.ModifiedTime.UTC().Format(time.RFC3339), source.Path)
			}
			return nil
		},
	}

	command.Flags().StringVar(&projectPath, "project-path", "", "Project path used for discovery context (default: current directory)")
	return command
}

func resolveProjectPath(path string) (string, error) {
	if path == "" {
		currentDir, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve current working directory: %w", err)
		}
		return filepath.Clean(currentDir), nil
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	if info, err := os.Stat(absolutePath); err != nil {
		return "", fmt.Errorf("validate project path %q: %w", absolutePath, err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("project path %q must be a directory", absolutePath)
	}

	return filepath.Clean(absolutePath), nil
}
