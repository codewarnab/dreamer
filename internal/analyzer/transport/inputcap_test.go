package transport

import "testing"

func TestCapInputBytes(t *testing.T) {
	const truncNote = "\n\n[truncated]\n"

	tests := []struct {
		name     string
		body     string
		maxBytes int
		want     string
	}{
		{
			name:     "body fits - no truncation",
			body:     "Short prompt",
			maxBytes: 100,
			want:     "Short prompt",
		},
		{
			name:     "header preserved, transcript truncated",
			body:     "System: analyze this\n\nChat transcript follows:\nUser: hello\nAssistant: hi\nUser: how are you\nAssistant: great",
			maxBytes: 80,
			want:     "System: analyze this\n\nChat transcript follows:\n\n\n[truncated]\nou\nAssistant: great",
		},
		{
			name:     "no marker - tail truncate entire body",
			body:     "This is a long body without the marker that needs truncation",
			maxBytes: 30,
			want:     "\n\n[truncated]\nneeds truncation",
		},
		{
			name:     "header too big - tail truncate",
			body:     "Very long system prompt that exceeds budget\n\nChat transcript follows:\nUser: test",
			maxBytes: 20,
			want:     "\n\n[truncated]\n: test",
		},
		{
			name:     "exact fit with marker",
			body:     "Header\n\nChat transcript follows:\nBody",
			maxBytes: 100,
			want:     "Header\n\nChat transcript follows:\nBody",
		},
		{
			name:     "maxBytes smaller than truncNote",
			body:     "Some content here",
			maxBytes: 14,
			want:     "\n\n[truncated]\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapInputBytes(tt.body, tt.maxBytes, truncNote)
			if got != tt.want {
				t.Errorf("CapInputBytes() = %q, want %q", got, tt.want)
			}
			if len(got) > tt.maxBytes {
				t.Errorf("CapInputBytes() length %d exceeds maxBytes %d", len(got), tt.maxBytes)
			}
		})
	}
}

func TestCapInputBytes_CustomTruncNote(t *testing.T) {
	body := "Header\n\nChat transcript follows:\n" + string(make([]byte, 1000))
	maxBytes := 100
	truncNote := "\n\n[custom truncation message]\n"

	cappedBody := CapInputBytes(body, maxBytes, truncNote)

	if len(cappedBody) > maxBytes {
		t.Errorf("cappedBody length %d exceeds maxBytes %d", len(cappedBody), maxBytes)
	}
	if cappedBody[:len("Header\n\nChat transcript follows:\n")] != "Header\n\nChat transcript follows:\n" {
		t.Error("header not preserved")
	}
	// Should contain the custom truncation note
	if cappedBody[len("Header\n\nChat transcript follows:\n"):len("Header\n\nChat transcript follows:\n")+len(truncNote)] != truncNote {
		t.Error("custom truncNote not present")
	}
}
