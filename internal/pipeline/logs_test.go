package pipeline

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"dreamer/internal/analyzer"
	"dreamer/internal/logging"
)

func TestLoggingSessionRecordsPromptAndResponse(t *testing.T) {
	logger, err := logging.New(t.TempDir(), "debug")
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}

	session := analyzer.NewLoggingSession(staticSession{response: "assistant response"}, logger, "fake-provider")
	if _, err := session.Run(context.Background(), "prompt body", time.Second); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	content := readFileString(t, logger.Path())
	assertContains(t, content, `level=info msg="provider call started"`)
	assertContains(t, content, `provider=fake-provider`)
	assertContains(t, content, `level=info msg="provider response"`)
	assertContains(t, content, `body="assistant response"`)
}

func TestLoggerPathConvention(t *testing.T) {
	outputRoot := t.TempDir()
	logger, err := logging.New(outputRoot, "info")
	if err != nil {
		t.Fatalf("logging.New returned error: %v", err)
	}
	defer logger.Close()

	if !strings.HasSuffix(logger.Path(), `logging\dreamer.log`) && !strings.HasSuffix(logger.Path(), "logging/dreamer.log") {
		t.Fatalf("logger path = %q, want logging/dreamer.log suffix", logger.Path())
	}
}

type staticSession struct {
	response string
}

func (s staticSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	return s.response, nil
}

func (s staticSession) Close() error {
	return nil
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %q: %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, content string, expected string) {
	t.Helper()
	if !strings.Contains(content, expected) {
		t.Fatalf("content missing %q\ncontent:\n%s", expected, content)
	}
}
