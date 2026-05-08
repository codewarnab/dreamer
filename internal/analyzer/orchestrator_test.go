package analyzer

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestOrchestratorAnalyzeParsesAndDeduplicatesFindings(t *testing.T) {
	rules := []AnalysisRule{
		{
			Category:       RuleCategoryBugs,
			PromptTemplate: "bugs:%s",
			Threshold:      0.70,
			Enabled:        true,
			Timeout:        5 * time.Second,
		},
		{
			Category:       RuleCategoryPerformance,
			PromptTemplate: "performance:%s",
			Threshold:      0.70,
			Enabled:        true,
			Timeout:        5 * time.Second,
		},
	}

	session := &fakeSession{
		responses: map[string]string{
			"bugs:chat context": `{"findings":[
				{"description":"Null pointer risk","confidence":0.9},
				{"description":"  null pointer risk  ","confidence":0.95}
			]}`,
			"performance:chat context": "```json\n[\n" +
				`{"description":"Inefficient loop","confidence":0.9},` + "\n" +
				`{"category":"PERFORMANCE","description":"inefficient loop","confidence":0.95},` + "\n" +
				`{"description":"Minor issue","confidence":0.3}` + "\n" +
				"]\n```",
		},
	}

	orchestrator := NewOrchestrator(rules)
	result, err := orchestrator.Analyze(context.Background(), session, "chat context")
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}

	if got, want := len(result.Findings), 2; got != want {
		t.Fatalf("len(findings) = %d, want %d; findings=%v", got, want, result.Findings)
	}

	if got, want := result.Findings[0].Category, RuleCategoryBugs; got != want {
		t.Fatalf("first finding category = %q, want %q", got, want)
	}
	if got, want := result.Findings[0].Description, "Null pointer risk"; got != want {
		t.Fatalf("first finding description = %q, want %q", got, want)
	}

	if got, want := result.Findings[1].Category, RuleCategoryPerformance; got != want {
		t.Fatalf("second finding category = %q, want %q", got, want)
	}
	if got, want := result.Findings[1].Description, "Inefficient loop"; got != want {
		t.Fatalf("second finding description = %q, want %q", got, want)
	}
}

func TestOrchestratorAnalyzeReturnsErrorOnInvalidJSON(t *testing.T) {
	rules := []AnalysisRule{
		{
			Category:       RuleCategoryBugs,
			PromptTemplate: "bugs:%s",
			Enabled:        true,
			Timeout:        5 * time.Second,
		},
	}

	session := &fakeSession{
		responses: map[string]string{
			"bugs:chat context": "not-json",
		},
	}

	orchestrator := NewOrchestrator(rules)
	_, err := orchestrator.Analyze(context.Background(), session, "chat context")
	if err == nil {
		t.Fatalf("Analyze expected error for invalid JSON response")
	}
}

type fakeSession struct {
	responses map[string]string
}

func (f *fakeSession) Run(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	response, ok := f.responses[prompt]
	if !ok {
		return "", fmt.Errorf("unexpected prompt: %q", prompt)
	}
	return response, nil
}

func (f *fakeSession) Close() error {
	return nil
}
