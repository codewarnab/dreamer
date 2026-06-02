package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

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
		return nil, fmt.Errorf("project %q not found in config", name)
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

// strBuilderWriter adapts strings.Builder to io.Writer for yaml.NewEncoder.
type strBuilderWriter struct{ b *strings.Builder }

func (w *strBuilderWriter) Write(p []byte) (int, error) {
	return w.b.Write(p)
}
