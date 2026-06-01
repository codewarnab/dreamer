package analyzer

import (
	"fmt"
	"regexp"
	"strings"
)

// RedactionPattern is one entry in the secret-redaction table.
type RedactionPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// RedactionResult counts how many hits each pattern produced. Matched content
// is intentionally never recorded.
type RedactionResult struct {
	HitsByName map[string]int
}

// TotalHits returns the sum of redaction hits across all patterns.
func (r RedactionResult) TotalHits() int {
	total := 0
	for _, count := range r.HitsByName {
		total += count
	}
	return total
}

// Redactor replaces secret-shaped text with [REDACTED:<type>] markers.
type Redactor struct {
	patterns []RedactionPattern
}

// NewRedactor builds a Redactor from the built-in patterns plus any
// user-supplied regex strings (each tagged `custom`).
func NewRedactor(extraPatterns []string) (*Redactor, error) {
	patterns := defaultRedactionPatterns()
	for _, raw := range extraPatterns {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		compiled, err := regexp.Compile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("compile custom redaction pattern %q: %w", trimmed, err)
		}
		patterns = append(patterns, RedactionPattern{Name: "custom", Pattern: compiled})
	}
	return &Redactor{patterns: patterns}, nil
}

// Redact returns the redacted text plus a per-pattern hit count.
func (r *Redactor) Redact(text string) (string, RedactionResult) {
	redactionResult := RedactionResult{HitsByName: map[string]int{}}
	if r == nil || len(r.patterns) == 0 || text == "" {
		return text, redactionResult
	}
	workingText := text
	for _, pattern := range r.patterns {
		marker := "[REDACTED:" + pattern.Name + "]"
		hits := 0
		workingText = pattern.Pattern.ReplaceAllStringFunc(workingText, func(string) string {
			hits++
			return marker
		})
		if hits > 0 {
			redactionResult.HitsByName[pattern.Name] += hits
		}
	}
	return workingText, redactionResult
}

func defaultRedactionPatterns() []RedactionPattern {
	return []RedactionPattern{
		{Name: "aws-access-key-id", Pattern: regexp.MustCompile(`\b(?:AKIA|ASIA|AIDA|AROA|AGPA|ANPA|ANVA|ABIA|ACCA)[0-9A-Z]{16}\b`)},
		{Name: "aws-secret-key", Pattern: regexp.MustCompile(`(?i)\baws[_\-]?(?:secret|sk)[_\-]?(?:access)?[_\-]?key\s*[:=]\s*[A-Za-z0-9/+=]{40}\b`)},
		{Name: "github-pat", Pattern: regexp.MustCompile(`\bghp_[A-Za-z0-9]{36,251}\b`)},
		{Name: "github-pat", Pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,255}\b`)},
		{Name: "gitlab-pat", Pattern: regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}\b`)},
		{Name: "jwt", Pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\b`)},
		{Name: "bearer", Pattern: regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9_\-\.=]+`)},
		{Name: "slack-token", Pattern: regexp.MustCompile(`\bxox[abps]-[A-Za-z0-9-]{10,}\b`)},
		{Name: "pem-private-key", Pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)},
		{Name: "password", Pattern: regexp.MustCompile(`(?i)\bpassword\s*[:=]\s*\S+`)},
		{Name: "secret", Pattern: regexp.MustCompile(`(?i)\bsecret\s*[:=]\s*\S+`)},
		{Name: "api-key", Pattern: regexp.MustCompile(`(?i)\bapi[_\-]?key\s*[:=]\s*\S+`)},
		{Name: "token", Pattern: regexp.MustCompile(`(?i)\btoken\s*[:=]\s*\S+`)},
		// Only match env lines with secret-bearing keywords to avoid
		// redacting PATH, HOME, LANG, TERM, SHELL, and other benign vars.
		// The keyword may appear anywhere in the var name (e.g. AWS_SECRET_ACCESS_KEY,
		// OPENAI_API_KEY, DB_PASSWORD, GH_TOKEN, STRIPE_SECRET_KEY).
		{Name: "env-line", Pattern: regexp.MustCompile(`(?m)^[A-Z0-9_]*(?:SECRET|TOKEN|KEY|PASSWORD|CREDENTIAL|AUTH|PRIVATE|API|URL|URI|DSN|CONN)[A-Z0-9_]*\s*=\s*\S.*$`)},
	}
}
