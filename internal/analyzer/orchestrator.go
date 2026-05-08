package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Finding struct {
	Category    RuleCategory `json:"category"`
	Description string       `json:"description"`
	Evidence    string       `json:"evidence,omitempty"`
	Confidence  float64      `json:"confidence,omitempty"`
}

type AnalysisResponse struct {
	Findings []Finding `json:"findings"`
}

type Orchestrator struct {
	rules []AnalysisRule
}

func NewOrchestrator(rules []AnalysisRule) *Orchestrator {
	if len(rules) == 0 {
		rules = DefaultRules()
	}

	copiedRules := make([]AnalysisRule, len(rules))
	copy(copiedRules, rules)

	return &Orchestrator{rules: copiedRules}
}

func (o *Orchestrator) Analyze(ctx context.Context, session Session, input string) (*AnalysisResponse, error) {
	if session == nil {
		return nil, fmt.Errorf("analysis session is required")
	}

	allFindings := make([]Finding, 0)
	for _, rule := range o.rules {
		if !rule.Enabled {
			continue
		}

		prompt := buildPrompt(rule.PromptTemplate, input)
		rawResponse, err := session.Run(ctx, prompt, rule.Timeout)
		if err != nil {
			return nil, fmt.Errorf("run %s rule: %w", rule.Category, err)
		}

		ruleFindings, err := parseRuleFindings(rawResponse, rule)
		if err != nil {
			return nil, fmt.Errorf("parse %s rule response: %w", rule.Category, err)
		}
		allFindings = append(allFindings, ruleFindings...)
	}

	return &AnalysisResponse{
		Findings: deduplicateFindings(allFindings),
	}, nil
}

func buildPrompt(promptTemplate string, input string) string {
	template := strings.TrimSpace(promptTemplate)
	if template == "" {
		return input
	}
	if strings.Contains(template, "{{input}}") {
		return strings.ReplaceAll(template, "{{input}}", input)
	}
	if strings.Contains(template, "%s") {
		return strings.ReplaceAll(template, "%s", input)
	}
	if strings.TrimSpace(input) == "" {
		return template
	}
	return template + "\n\n" + input
}

func parseRuleFindings(raw string, rule AnalysisRule) ([]Finding, error) {
	payload := stripCodeFence(raw)
	if payload == "" {
		return nil, nil
	}

	findings, err := decodeFindingsPayload(payload)
	if err != nil {
		return nil, err
	}

	normalizedFindings := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		description := strings.TrimSpace(finding.Description)
		if description == "" {
			continue
		}

		if rule.Threshold > 0 && finding.Confidence > 0 && finding.Confidence < rule.Threshold {
			continue
		}

		category := normalizeCategory(finding.Category)
		if category == "" {
			category = rule.Category
		}

		normalizedFindings = append(normalizedFindings, Finding{
			Category:    category,
			Description: description,
			Evidence:    strings.TrimSpace(finding.Evidence),
			Confidence:  finding.Confidence,
		})
	}

	return normalizedFindings, nil
}

func decodeFindingsPayload(payload string) ([]Finding, error) {
	var wrapped AnalysisResponse
	if err := json.Unmarshal([]byte(payload), &wrapped); err == nil && wrapped.Findings != nil {
		return wrapped.Findings, nil
	}

	var findings []Finding
	if err := json.Unmarshal([]byte(payload), &findings); err == nil {
		return findings, nil
	} else {
		return nil, fmt.Errorf("invalid findings JSON: %w", err)
	}
}

func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) == 0 {
		return trimmed
	}

	if strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1]
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func deduplicateFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	deduplicated := make([]Finding, 0, len(findings))

	for _, finding := range findings {
		key := dedupKey(finding)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduplicated = append(deduplicated, finding)
	}

	return deduplicated
}

func dedupKey(finding Finding) string {
	return strings.ToLower(strings.TrimSpace(string(finding.Category))) + "|" + strings.ToLower(strings.TrimSpace(finding.Description))
}

func normalizeCategory(category RuleCategory) RuleCategory {
	switch strings.ToLower(strings.TrimSpace(string(category))) {
	case string(RuleCategoryBugs):
		return RuleCategoryBugs
	case string(RuleCategoryPerformance):
		return RuleCategoryPerformance
	case string(RuleCategoryDuplication):
		return RuleCategoryDuplication
	case string(RuleCategoryMissingTests):
		return RuleCategoryMissingTests
	case string(RuleCategoryArchitecture):
		return RuleCategoryArchitecture
	case string(RuleCategoryDocumentation):
		return RuleCategoryDocumentation
	case string(RuleCategoryLint):
		return RuleCategoryLint
	case string(RuleCategorySecurity):
		return RuleCategorySecurity
	case string(RuleCategoryTypes):
		return RuleCategoryTypes
	default:
		return RuleCategory(strings.ToLower(strings.TrimSpace(string(category))))
	}
}
