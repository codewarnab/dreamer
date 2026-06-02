package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const removeSeedConfig = `# dreamer global config.

default_provider: openclaude-cli

# Projects iterated by the daemon.
projects:
  - name: alpha
    path: /abs/alpha
    since: 24h
  - name: beta
    path: /abs/beta
    since: 7d
`

func TestRemoveProjectFromYAML_RemovesProject(t *testing.T) {
	out, err := RemoveProjectFromYAML([]byte(removeSeedConfig), "alpha")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "name: alpha") {
		t.Fatalf("alpha should be removed:\n%s", s)
	}
	if !strings.Contains(s, "name: beta") {
		t.Fatalf("beta should remain:\n%s", s)
	}
	if !strings.Contains(s, "# Projects iterated by the daemon.") {
		t.Fatalf("comments lost:\n%s", s)
	}
	if !strings.Contains(s, "default_provider: openclaude-cli") {
		t.Fatalf("other keys lost:\n%s", s)
	}
}

func TestRemoveProjectFromYAML_RemovesLastProject(t *testing.T) {
	seed := `projects:
  - name: only
    path: /abs/only
    since: 24h
`
	out, err := RemoveProjectFromYAML([]byte(seed), "only")
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "name: only") {
		t.Fatalf("only should be removed:\n%s", s)
	}
	if !strings.Contains(s, "projects:") {
		t.Fatalf("projects key should remain:\n%s", s)
	}
}

func TestRemoveProjectFromYAML_ProjectNotFound(t *testing.T) {
	_, err := RemoveProjectFromYAML([]byte(removeSeedConfig), "nonexistent")
	if err == nil {
		t.Fatalf("expected error for missing project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error %q should mention not found", err)
	}
}

func TestRemoveProjectFromYAML_EmptyName(t *testing.T) {
	_, err := RemoveProjectFromYAML([]byte(removeSeedConfig), "")
	if err == nil {
		t.Fatalf("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error %q should mention required", err)
	}
}

func TestRemoveProjectFromYAML_NoProjectsKey(t *testing.T) {
	_, err := RemoveProjectFromYAML([]byte("other: value\n"), "alpha")
	if err == nil {
		t.Fatalf("expected error when no projects key")
	}
}

func TestRemoveProjectFromYAML_ProjectsNotList(t *testing.T) {
	_, err := RemoveProjectFromYAML([]byte("projects: not-a-list\n"), "alpha")
	if err == nil {
		t.Fatalf("expected error when projects is not a list")
	}
}

func TestRemoveProjectFromYAML_NullProjects(t *testing.T) {
	// `projects: null` parses as a ScalarNode, exercising the null-value
	// branch (distinct from `projects: []`, which is an empty SequenceNode and
	// falls through to the "not found" path covered below).
	_, err := RemoveProjectFromYAML([]byte("projects: null\n"), "alpha")
	if err == nil {
		t.Fatalf("expected error for null projects value")
	}
	if !strings.Contains(err.Error(), "no projects configured") {
		t.Fatalf("expected 'no projects configured' error, got: %v", err)
	}
}

func TestRemoveProjectFromYAML_EmptySequence(t *testing.T) {
	// `projects: []` is a valid but empty sequence: the named project simply
	// isn't present, so removal reports it as not found.
	_, err := RemoveProjectFromYAML([]byte("projects: []\n"), "alpha")
	if err == nil {
		t.Fatalf("expected error for empty projects list")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestFindMappingChild(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("a: 1\nb: 2\n"), &root); err != nil {
		t.Fatal(err)
	}
	doc := root.Content[0]
	k, v := FindMappingChild(doc, "b")
	if k == nil || k.Value != "b" {
		t.Fatalf("expected key 'b'")
	}
	if v == nil || v.Value != "2" {
		t.Fatalf("expected value '2'")
	}
	_, nilV := FindMappingChild(doc, "missing")
	if nilV != nil {
		t.Fatalf("expected nil for missing key")
	}
}
