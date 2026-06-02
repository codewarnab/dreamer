package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"dreamer/internal/config"
	"dreamer/internal/fsutil"
)

func newAddCommand() *cobra.Command {
	var (
		name  string
		since string
	)
	cmd := &cobra.Command{
		Use:   "add [path]",
		Short: "Add a project to dreamer's config (path defaults to current directory).",
		Long: "add appends an entry to the 'projects:' list in <UserConfigDir>/dreamer/config.yaml.\n\n" +
			"path defaults to '.' (current working directory). The path is resolved to an absolute\n" +
			"path; --name defaults to the basename; --since defaults to '24h'. Comments and other\n" +
			"keys in config.yaml are preserved via the yaml.v3 Node API.\n\n" +
			"If config.yaml does not yet exist, run 'dreamer setup' first.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pathArg := "."
			if len(args) == 1 {
				pathArg = args[0]
			}
			absPath, err := resolveAddPath(pathArg)
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(absPath)
			}
			cfgPath, err := resolveConfigPath(configPath)
			if err != nil {
				return err
			}
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				return fmt.Errorf("config file %q does not exist; run 'dreamer setup' first", cfgPath)
			} else if err != nil {
				return fmt.Errorf("stat config %q: %w", cfgPath, err)
			}
			configBytes, err := os.ReadFile(cfgPath)
			if err != nil {
				return fmt.Errorf("read config %q: %w", cfgPath, err)
			}
			updated, err := appendProjectToYAML(configBytes, name, absPath, since)
			if err != nil {
				return err
			}
			// SecretPerms (0600): config.yaml may carry provider passwords;
			// keep it consistent with `dreamer remove` and the web writers.
			if err := fsutil.WriteFileAtomic(cfgPath, updated, fsutil.SecretPerms); err != nil {
				return fmt.Errorf("write config %q: %w", cfgPath, err)
			}
			cmd.Printf("added project %q (path=%s since=%s) to %s\n", name, absPath, since, cfgPath)
			return nil
		},
	}
	cmd.Flags().StringVarP(&name, "name", "n", "", "Project name (default: basename of path).")
	cmd.Flags().StringVarP(&since, "since", "s", config.DefaultSince, "Lookback window for chat history (e.g. 30m, 1h, 1d, 1w, 1mo, lifetime)")
	return cmd
}

// resolveAddPath turns the CLI arg into an absolute, existing directory path.
func resolveAddPath(pathArg string) (string, error) {
	pathArg = strings.TrimSpace(pathArg)
	if pathArg == "" {
		pathArg = "."
	}
	expanded, err := config.ExpandUserHome(pathArg)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", pathArg, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", abs)
	}
	return abs, nil
}

// appendProjectToYAML rewrites the projects: list to include the new
// entry while preserving every comment and unrelated key. Returns the
// rendered bytes.
func appendProjectToYAML(configBytes []byte, name, path, since string) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(configBytes, &root); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("config yaml root is not a document")
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config yaml root is not a mapping")
	}

	projectsKey, projectsVal := config.FindMappingChild(doc, "projects")
	entry := projectMappingNode(name, path, since)

	if projectsVal == nil {
		// projects: key missing entirely — insert a fresh sequence.
		doc.Content = append(doc.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "projects"},
			&yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{entry}},
		)
	} else if projectsVal.Kind == yaml.SequenceNode {
		// Duplicate detection by name OR path — refuse to add the same project twice.
		if dup := findDuplicateProject(projectsVal, name, path); dup != "" {
			return nil, fmt.Errorf("project already configured: %s", dup)
		}
		projectsVal.Style = 0 // force block style so multi-entry lists render line-per-entry
		projectsVal.Content = append(projectsVal.Content, entry)
	} else if projectsVal.Kind == yaml.ScalarNode && (projectsVal.Value == "" || projectsVal.Value == "[]" || projectsVal.Tag == "!!null") {
		// projects: [] (or null) — replace with a block-style sequence.
		*projectsVal = yaml.Node{Kind: yaml.SequenceNode, Style: 0, Content: []*yaml.Node{entry}}
	} else {
		return nil, fmt.Errorf("projects: in config is not a list (kind=%v, value=%q)", projectsVal.Kind, projectsVal.Value)
	}
	_ = projectsKey

	var buf strings.Builder
	yamlEncoder := yaml.NewEncoder(&strBuilderWriter{b: &buf})
	yamlEncoder.SetIndent(2)
	if err := yamlEncoder.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode config yaml: %w", err)
	}
	if err := yamlEncoder.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return []byte(buf.String()), nil
}

func findDuplicateProject(seq *yaml.Node, name, path string) string {
	// Normalize the input path so ~/foo, /abs/x, and C:\abs\x all
	// compare consistently regardless of how they were typed.
	resolvedPath := fsutil.CanonicalPath(path)
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		_, nameNode := config.FindMappingChild(item, "name")
		_, pathNode := config.FindMappingChild(item, "path")
		if nameNode != nil && nameNode.Value == name {
			return fmt.Sprintf("name=%q", name)
		}
		if pathNode != nil {
			if fsutil.CanonicalPath(pathNode.Value) == resolvedPath {
				return fmt.Sprintf("path=%q", path)
			}
		}
	}
	return ""
}

func projectMappingNode(name, path, since string) *yaml.Node {
	scalar := func(v string) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Value: v}
	}
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			scalar("name"), scalar(name),
			scalar("path"), scalar(path),
			scalar("since"), scalar(since),
		},
	}
}

// strBuilderWriter adapts strings.Builder to io.Writer for yaml.NewEncoder.
type strBuilderWriter struct{ b *strings.Builder }

func (w *strBuilderWriter) Write(p []byte) (int, error) {
	return w.b.Write(p)
}
