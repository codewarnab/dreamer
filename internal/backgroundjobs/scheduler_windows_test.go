//go:build windows

package backgroundjobs

import (
	"fmt"
	"strings"
	"testing"

	"dreamer/internal/logging"
)

func TestStripControlChars(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "hello world", "hello world"},
		{"newline", "hello\nworld", "hello\nworld"},  // \n is valid XML whitespace
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
		{"quote", `a"b`, "a&#34;b"}, // Go xml.EscapeText uses numeric entities
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

func TestClassifyScheduleError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want scheduleErrorCategory
	}{
		{"nil", nil, scheduleErrNone},
		{"access denied", fmt.Errorf("Access is denied."), scheduleErrPermission},
		{"permission keyword", fmt.Errorf("permission denied for task"), scheduleErrPermission},
		{"file not found", fmt.Errorf("The system cannot find the file specified."), scheduleErrNotFound},
		{"not found keyword", fmt.Errorf("task not found in scheduler"), scheduleErrNotFound},
		{"does not exist", fmt.Errorf("The task does not exist"), scheduleErrNotFound},
		{"xml error", fmt.Errorf("Invalid XML content"), scheduleErrXML},
		{"invalid keyword", fmt.Errorf("invalid schedule format"), scheduleErrXML},
		{"unrecognized", fmt.Errorf("some random error"), scheduleErrUnknown},
		{"empty error", fmt.Errorf(""), scheduleErrUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyScheduleError(tt.err)
			if got != tt.want {
				t.Errorf("classifyScheduleError(%v) = %v (%s), want %v (%s)",
					tt.err, got, got.Error(), tt.want, tt.want.Error())
			}
		})
	}
}

func TestScheduleErrorCategory_ErrorString(t *testing.T) {
	// Verify all categories have non-empty error strings.
	categories := []scheduleErrorCategory{
		scheduleErrNone, scheduleErrPermission, scheduleErrNotFound,
		scheduleErrXML, scheduleErrUnknown,
	}
	for _, c := range categories {
		if c.Error() == "" {
			t.Errorf("category %d has empty Error() string", c)
		}
	}
}

func TestParseWindowsTime(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"ISO basic", "2026-05-27T09:00:00", false},
		{"ISO with fractional", "2026-05-27T09:00:00.123456789", false},
		{"RFC3339", "2026-05-27T09:00:00+05:30", false},
		{"US format", "5/27/2026 9:00:00 AM", false},
		{"unparseable", "not a time", true},
		{"empty", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseWindowsTime(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseWindowsTime(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestWeekdayToXMLElement(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"monday", "Monday"}, {"tuesday", "Tuesday"}, {"wednesday", "Wednesday"},
		{"thursday", "Thursday"}, {"friday", "Friday"}, {"saturday", "Saturday"},
		{"sunday", "Sunday"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := weekdayToXMLElement(tt.input)
			if got != tt.want {
				t.Errorf("weekdayToXMLElement(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWeekdayToXMLElement_UnknownPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("weekdayToXMLElement(\"unknown\") did not panic")
		}
	}()
	weekdayToXMLElement("unknown")
}

// B14: Description with special XML characters should be properly escaped.
func TestBuildTaskXML_DescriptionEscaped(t *testing.T) {
	s := &windowsScheduler{
		cfg: SchedulerConfig{
			StoreDir:       t.TempDir(),
			ExecutablePath: `C:\Program Files\dreamer.exe`,
			ConfigPath:     `C:\Users\test\.config\dreamer\config.yaml`,
			InstallID:      "test-install",
			ConfigHash:     "abc123",
			ExecHash:       "def456",
		},
		logger: logging.Silent(),
	}

	params := ScheduleParams{
		JobID:    "aabbccdd11223344",
		Schedule: ScheduleSpec{Kind: ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"},
		Name:     "Test <script>alert('xss')</script>",
		Enabled:  true,
	}

	xmlBytes, err := s.buildTaskXML(params)
	if err != nil {
		t.Fatalf("buildTaskXML error: %v", err)
	}

	xmlStr := string(xmlBytes)

	// The Description field should contain XML-escaped content.
	// Description is built from hex hashes (job ID, install ID, config hash, spec hash, exec hash),
	// so it should not contain any XML-special characters.
	if strings.Contains(xmlStr, "<script>") {
		t.Error("Description contains unescaped <script> tag")
	}
	// Verify the description contains the job ID.
	if !strings.Contains(xmlStr, "dreamer:job_id=aabbccdd11223344") {
		t.Error("Description should contain the job ID")
	}
}

func TestBuildTriggerXML_HourlyEvery(t *testing.T) {
	tests := []struct {
		name         string
		every        string
		wantInterval string
	}{
		{"default", "", "PT1H"},
		{"5m", "5m", "PT5M"},
		{"15m", "15m", "PT15M"},
		{"45m", "45m", "PT45M"},
		{"2h", "2h", "PT2H"},
		{"90m", "90m", "PT90M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := ScheduleSpec{Kind: ScheduleInterval, Every: tt.every, Timezone: "UTC"}
			xmlStr, err := buildTriggerXML(spec)
			if err != nil {
				t.Fatalf("buildTriggerXML error: %v", err)
			}
			want := "<Interval>" + tt.wantInterval + "</Interval>"
			if !strings.Contains(xmlStr, want) {
				t.Errorf("buildTriggerXML(every=%q)\n  got:  %s\n  want: %s", tt.every, xmlStr, want)
			}
		})
	}
}
