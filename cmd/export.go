package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
	"dreamer/internal/output"
	"dreamer/internal/pipeline"
)

func newExportCommand() *cobra.Command {
	var (
		projectPath string
		projectName string
		outputPath  string
	)

	cmd := &cobra.Command{
		Use:   "export [flags]",
		Short: "Export project todos.md directly to the target project directory.",
		Long: "export copies the synthesized todos.md for a project into the target\n" +
			"project repository (or an explicit destination path). Useful for committing\n" +
			"prevention guardrails directly into version control.\n" +
			"Overwrites the destination todos.md if it already exists.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedConfigPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			overlayPath, _ := config.GlobalOverlayPath()
			appConfig, err := config.LoadConfigWithOverlay(resolvedConfigPath, overlayPath)
			if err != nil {
				return fmt.Errorf("load config %q: %w", resolvedConfigPath, err)
			}

			// If path is not specified, try current working directory.
			if strings.TrimSpace(projectPath) == "" && strings.TrimSpace(projectName) == "" {
				cwd, err := os.Getwd()
				if err == nil {
					projectPath = cwd
				}
			}

			resolvedPath := strings.TrimSpace(projectPath)
			resolvedName := strings.TrimSpace(projectName)

			if resolvedName != "" {
				for _, p := range appConfig.Projects {
					if p.Name == resolvedName {
						if resolvedPath == "" && p.Path != "" {
							resolvedPath = p.Path
						}
						break
					}
				}
			}

			if resolvedPath != "" {
				absPath, err := filepath.Abs(resolvedPath)
				if err == nil {
					resolvedPath = absPath
				}
				// Fail fast when the user explicitly passed --path pointing at a
				// non-existent directory. Without this, the command falls through
				// to DeriveProjectName and reports a misleading "no todos found"
				// error for a derived name instead of the real problem.
				if strings.TrimSpace(projectPath) != "" {
					if info, err := os.Stat(resolvedPath); err != nil || !info.IsDir() {
						return fmt.Errorf("project directory does not exist: %q; specify a valid --path or --project", resolvedPath)
					}
				}
			}

			// Match project in config if possible
			if resolvedName == "" && resolvedPath != "" {
				canonResolved := fsutil.CanonicalPath(resolvedPath)
				for _, p := range appConfig.Projects {
					if fsutil.CanonicalPath(p.Path) == canonResolved {
						resolvedName = p.Name
						break
					}
				}
			}

			if resolvedName == "" && resolvedPath != "" {
				usedNames := make(map[string]string)
				for _, p := range appConfig.Projects {
					if p.Name != "" && p.Path != "" {
						usedNames[p.Name] = p.Path
					}
				}
				resolvedName = pipeline.DeriveProjectName(resolvedPath, usedNames)
			}

			if resolvedName == "" {
				return fmt.Errorf("could not determine project name; specify --path or --project")
			}

			outputRoot := strings.TrimSpace(appConfig.Daemon.OutputRoot)
			srcTodosPath, err := output.TodosPath(outputRoot, resolvedName)
			if err != nil {
				return fmt.Errorf("resolve source todos path: %w", err)
			}

			data, err := os.ReadFile(srcTodosPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no todos found for project %q (%s); run 'dreamer analyze --path %s' first", resolvedName, srcTodosPath, resolvedPath)
				}
				return fmt.Errorf("read source todos %q: %w", srcTodosPath, err)
			}

			dest := strings.TrimSpace(outputPath)
			if dest == "" {
				if resolvedPath == "" {
					return fmt.Errorf("target project directory unknown; specify --path or --output")
				}
				dest = filepath.Join(resolvedPath, output.TodosFileName)
			} else {
				absDest, err := filepath.Abs(dest)
				if err == nil {
					dest = absDest
				}
			}

			if err := os.MkdirAll(filepath.Dir(dest), fsutil.DirPerms); err != nil {
				return fmt.Errorf("create destination directory: %w", err)
			}

			if err := fsutil.WriteFileAtomic(dest, data, fsutil.FilePerms); err != nil {
				return fmt.Errorf("write todos to %q: %w", dest, err)
			}

			cmd.Printf("exported todos for project %s to %s\n", resolvedName, dest)
			return nil
		},
	}

	cmd.Flags().StringVar(&projectPath, "path", "", "Target project directory")
	cmd.Flags().StringVar(&projectName, "project", "", "Target project name from config")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Destination file path (default: <project-path>/todos.md)")

	return cmd
}
