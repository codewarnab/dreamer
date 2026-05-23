package flagutil

import (
	"reflect"
	"testing"
)

func TestReplaceFlag(t *testing.T) {
	args := []string{"--permission-mode", "plan", "--tools", "Read,Grep"}
	got := ReplaceFlag(args, "--permission-mode", "plan", "default")
	want := []string{"--permission-mode", "default", "--tools", "Read,Grep"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReplaceFlag_NoMatch(t *testing.T) {
	args := []string{"--permission-mode", "default", "--tools", "Read"}
	got := ReplaceFlag(args, "--permission-mode", "plan", "bypassPermissions")
	if !reflect.DeepEqual(got, args) {
		t.Errorf("expected unchanged args, got %v", got)
	}
}

func TestAppendToFlag(t *testing.T) {
	args := []string{"--tools", "Read,Grep,Glob"}
	got := AppendToFlag(args, "--tools", "mcp__dreamer__record_finding")
	want := []string{"--tools", "Read,Grep,Glob,mcp__dreamer__record_finding"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestAppendToFlag_NoMatch(t *testing.T) {
	args := []string{"--model", "gpt-4"}
	got := AppendToFlag(args, "--tools", "Bash")
	if !reflect.DeepEqual(got, args) {
		t.Errorf("expected unchanged args, got %v", got)
	}
}

func TestRemoveFlag(t *testing.T) {
	args := []string{"--approval-mode", "plan", "--model", "gemini"}
	got := RemoveFlag(args, "--approval-mode")
	want := []string{"--model", "gemini"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveFlag_CombinedForm(t *testing.T) {
	args := []string{"--approval-mode=plan", "--model", "gemini"}
	got := RemoveFlag(args, "--approval-mode")
	want := []string{"--model", "gemini"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRemoveFlag_NotFound(t *testing.T) {
	args := []string{"--model", "gemini"}
	got := RemoveFlag(args, "--approval-mode")
	if !reflect.DeepEqual(got, args) {
		t.Errorf("expected unchanged args, got %v", got)
	}
}
