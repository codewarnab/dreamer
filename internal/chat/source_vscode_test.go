package chat

import "testing"

func TestDecodeVSCodePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain unix", "file:///home/user/project", "/home/user/project"},
		{"windows drive", "file:///C:/Users/test/project", "C:/Users/test/project"},
		{"percent-encoded space", "file:///C:/Users/My%20Project", "C:/Users/My Project"},
		{"percent-encoded colon", "file:///C%3A/Users/test", "C:/Users/test"},
		{"no scheme", "/home/user/project", "/home/user/project"},
		{"percent-encoded unix", "file:///home/user/my%20project", "/home/user/my project"},
		{"UNC path", "file://server/share", "server/share"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeVSCodePath(tt.input)
			if got != tt.want {
				t.Errorf("decodeVSCodePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
