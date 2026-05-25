package chat

import "testing"

func TestDecodeVSCodePath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantOk  bool
	}{
		{"plain unix", "file:///home/user/project", "/home/user/project", true},
		{"windows drive", "file:///C:/Users/test/project", "C:/Users/test/project", true},
		{"percent-encoded space", "file:///C:/Users/My%20Project", "C:/Users/My Project", true},
		{"percent-encoded colon", "file:///C%3A/Users/test", "C:/Users/test", true},
		{"no scheme", "/home/user/project", "/home/user/project", true},
		{"percent-encoded unix", "file:///home/user/my%20project", "/home/user/my project", true},
		{"invalid percent encoding", "file:///C:/bad%ZZpath", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := decodeVSCodePath(tt.input)
			if ok != tt.wantOk {
				t.Errorf("decodeVSCodePath(%q) ok = %v, want %v", tt.input, ok, tt.wantOk)
			}
			if got != tt.want {
				t.Errorf("decodeVSCodePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
