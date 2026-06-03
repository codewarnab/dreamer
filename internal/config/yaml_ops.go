package config

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"

	"dreamer/internal/fsutil"
)

// ErrProjectNotFound indicates that the specified project was not found in the config.
// By wrapping this standard sentinel error, handlers can check errors.Is(err, ErrProjectNotFound)
// to return a clear HTTP 404 (Not Found) instead of a generic HTTP 500 (Internal Server Error).
var ErrProjectNotFound = errors.New("project not found")

// FindMappingChild searches a yaml.MappingNode for a key matching the
// given string and returns both the key and value nodes. Returns (nil, nil)
// when the key is absent. Exported so cmd/add and cmd/remove share one
// implementation instead of duplicating the traversal.
func FindMappingChild(node *yaml.Node, key string) (k, v *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i], node.Content[i+1]
		}
	}
	return nil, nil
}

// RemoveProjectFromYAML removes the project with the given name from the
// projects: sequence in the YAML config, preserving all comments and
// unrelated keys.
//
// Preconditions: configBytes must be valid YAML with a document mapping root.
// Postconditions: returned bytes are valid YAML; the named project is absent
// from the projects: sequence.
//
// Returns an error if the YAML structure is unexpected, no projects: key
// exists, or no project matches the given name.
func RemoveProjectFromYAML(configBytes []byte, name string) ([]byte, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("project name is required")
	}

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

	_, projectsVal := FindMappingChild(doc, "projects")
	if projectsVal == nil {
		return nil, fmt.Errorf("no projects configured in config")
	}
	if projectsVal.Kind != yaml.SequenceNode {
		if projectsVal.Kind == yaml.ScalarNode && (projectsVal.Value == "" || projectsVal.Value == "[]" || projectsVal.Tag == "!!null") {
			return nil, fmt.Errorf("no projects configured in config")
		}
		return nil, fmt.Errorf("projects: in config is not a list (kind=%v, value=%q)", projectsVal.Kind, projectsVal.Value)
	}

	// Find the project by name.
	idx := -1
	for i, item := range projectsVal.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		_, nameNode := FindMappingChild(item, "name")
		if nameNode != nil && nameNode.Value == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("project %q: %w", name, ErrProjectNotFound)
	}

	// Remove the project from the sequence.
	projectsVal.Content = append(projectsVal.Content[:idx], projectsVal.Content[idx+1:]...)

	var buf strings.Builder
	enc := yaml.NewEncoder(&strBuilderWriter{b: &buf})
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode config yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return []byte(buf.String()), nil
}

// AppendProjectToYAML rewrites the projects: list to include the new
// entry while preserving every comment and unrelated key. Returns the
// rendered bytes.
// It directly manipulates the yaml.Node AST. This ensures that layout,
// whitespace, and comments are fully preserved, which standard struct unmarshaling/marshaling would lose.
func AppendProjectToYAML(configBytes []byte, name, path, since string) ([]byte, error) {
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

	_, projectsVal := FindMappingChild(doc, "projects")
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

	var buf strings.Builder
	enc := yaml.NewEncoder(&strBuilderWriter{b: &buf})
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return nil, fmt.Errorf("encode config yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return []byte(buf.String()), nil
}

func findDuplicateProject(seq *yaml.Node, name, path string) string {
	// Normalize the input path so ~/foo, /abs/x, and C:\abs\x all
	// compare consistently regardless of how they were typed.
	// This uses fsutil.CanonicalPath which handles path casing (on Windows),
	// slashes/backslashes normalization, and directory structures.
	resolvedPath := fsutil.CanonicalPath(path)
	for _, item := range seq.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		_, nameNode := FindMappingChild(item, "name")
		_, pathNode := FindMappingChild(item, "path")
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
