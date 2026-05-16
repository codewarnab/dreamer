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
	result := RedactionResult{HitsByName: map[string]int{}}
	if r == nil || len(r.patterns) == 0 || text == "" {
		return text, result
	}
	current := text
	for _, p := range r.patterns {
		marker := "[REDACTED:" + p.Name + "]"
		hits := 0
		current = p.Pattern.ReplaceAllStringFunc(current, func(string) string {
			hits++
			return marker
		})
		if hits > 0 {
			result.HitsByName[p.Name] += hits
		}
	}
	return current, result
}

// MergeRedactionResult adds the counts of next into dst.
func MergeRedactionResult(dst RedactionResult, next RedactionResult) RedactionResult {
	if dst.HitsByName == nil {
		dst.HitsByName = map[string]int{}
	}
	for name, count := range next.HitsByName {
		dst.HitsByName[name] += count
	}
	return dst
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
		{Name: "env-line", Pattern: regexp.MustCompile(`(?m)^[A-Z][A-Z0-9_]+\s*=\s*[^\s].+$`)},
	}
}
