package flagutil

import (
	"reflect"
	"testing"
)

func TestReplaceFlag(t *testing.T) {
	cases := []struct {
		name             string
		args             []string
		flag, oldV, newV string
		want             []string
	}{
		{
			name: "space form match",
			args: []string{"claude", "--permission-mode", "plan", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--permission-mode", "default", "--add-dir", "/x"},
		},
		{
			name: "equals form match",
			args: []string{"claude", "--permission-mode=plan", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--permission-mode=default", "--add-dir", "/x"},
		},
		{
			name: "no match leaves args alone",
			args: []string{"claude", "--add-dir", "/x"},
			flag: "--permission-mode", oldV: "plan", newV: "default",
			want: []string{"claude", "--add-dir", "/x"},
		},
		{
			name: "wrong old value skipped",
			args: []string{"claude", "--permission-mode", "default"},
			flag: "--permission-mode", oldV: "plan", newV: "yolo",
			want: []string{"claude", "--permission-mode", "default"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReplaceFlag(append([]string(nil), c.args...), c.flag, c.oldV, c.newV)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestAppendToFlag(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		flag, value string
		want        []string
	}{
		{
			name: "space form append to non-empty",
			args: []string{"gemini", "--tools", "Read,Grep"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools", "Read,Grep,Bash"},
		},
		{
			name: "equals form append to non-empty",
			args: []string{"gemini", "--tools=Read,Grep"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools=Read,Grep,Bash"},
		},
		{
			name: "empty current value: no leading comma",
			args: []string{"gemini", "--tools", ""},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools", "Bash"},
		},
		{
			name: "equals form empty value: no leading comma",
			args: []string{"gemini", "--tools="},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--tools=Bash"},
		},
		{
			name: "empty suffix is a no-op",
			args: []string{"gemini", "--tools", "Read"},
			flag: "--tools", value: "",
			want: []string{"gemini", "--tools", "Read"},
		},
		{
			name: "missing flag is a no-op",
			args: []string{"gemini", "--add-dir", "/x"},
			flag: "--tools", value: "Bash",
			want: []string{"gemini", "--add-dir", "/x"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AppendToFlag(append([]string(nil), c.args...), c.flag, c.value)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestRemoveFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		flag string
		want []string
	}{
		{
			name: "space form removed",
			args: []string{"gemini", "--approval-mode", "plan", "--add-dir", "/x"},
			flag: "--approval-mode",
			want: []string{"gemini", "--add-dir", "/x"},
		},
		{
			name: "equals form removed",
			args: []string{"gemini", "--approval-mode=plan", "--add-dir", "/x"},
			flag: "--approval-mode",
			want: []string{"gemini", "--add-dir", "/x"},
		},
		{
			name: "double occurrence removed",
			args: []string{"gemini", "--approval-mode", "plan", "--approval-mode", "yolo"},
			flag: "--approval-mode",
			want: []string{"gemini"},
		},
		{
			name: "value starting with dash still consumed",
			args: []string{"cmd", "--threshold", "-1", "--keep"},
			flag: "--threshold",
			want: []string{"cmd", "--keep"},
		},
		{
			name: "prefix-collision flag survives",
			args: []string{"cmd", "--approval-mode-extra", "keep", "--approval-mode", "plan"},
			flag: "--approval-mode",
			want: []string{"cmd", "--approval-mode-extra", "keep"},
		},
		{
			name: "flag absent leaves args alone",
			args: []string{"cmd", "--keep"},
			flag: "--gone",
			want: []string{"cmd", "--keep"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RemoveFlag(append([]string(nil), c.args...), c.flag)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}
