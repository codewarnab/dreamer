//go:build windows

package backgroundjobs

import "testing"

func TestStripControlChars(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "hello world", "hello world"},
		{"newline", "hello\nworld", "hello\nworld"},   // \n is valid XML whitespace
		{"crlf", "hello\r\nworld", "hello\r\nworld"}, // \r is valid XML whitespace
		{"null", "hello\x00world", "helloworld"},
		{"tab", "hello\tworld", "hello\tworld"}, // tab is valid XML
		{"control", "hello\x01world", "helloworld"},
		{"delete", "hello\x7Fworld", "helloworld"},
		{"mixed", "a\x00b\x01c\td", "abc\td"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripControlChars(tt.in)
			if got != tt.want {
				t.Errorf("stripControlChars(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestXMLFieldEscaping(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"clean", "hello", "hello"},
		{"ampersand", "a&b", "a&amp;b"},
		{"less", "a<b", "a&lt;b"},
		{"greater", "a>b", "a&gt;b"},
		{"quote", `a"b`, "a&#34;b"},            // Go xml.EscapeText uses numeric entities
		{"apostrophe", "a'b", "a&#39;b"},
		{"mixed", `<a&b>"c`, "&lt;a&amp;b&gt;&#34;c"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := xmlEscapeText(tt.value)
			if got != tt.want {
				t.Errorf("xmlEscapeText(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestExtractXMLField(t *testing.T) {
	xml := `<Task><RegistrationInfo><Description>test-desc</Description></RegistrationInfo></Task>`

	tests := []struct {
		name  string
		field string
		want  string
	}{
		{"found", "Description", "test-desc"},
		{"not found", "Author", ""},
		{"empty field", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractXMLField(xml, tt.field)
			if got != tt.want {
				t.Errorf("extractXMLField(%q, %q) = %q, want %q", xml, tt.field, got, tt.want)
			}
		})
	}
}
