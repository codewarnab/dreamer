package lintrules

import "testing"

func TestIsAllowed_GolangCI(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		rule    string
		allowed bool
	}{
		{"staticcheck", true},
		{"govet", true},
		{"errcheck", true},
		{"gosec", true},
		{"revive", true},
		{"nonexistent-linter", false},
	}
	for _, tt := range tests {
		allowed, known := v.IsAllowed("golangci-lint", tt.rule)
		if allowed != tt.allowed {
			t.Errorf("golangci-lint/%s: allowed=%v, want %v", tt.rule, allowed, tt.allowed)
		}
		if !known {
			t.Errorf("golangci-lint/%s: known=false, want true", tt.rule)
		}
	}
}

func TestIsAllowed_GolangCIAlias(t *testing.T) {
	v := NewValidator()
	allowed, known := v.IsAllowed("golangci", "govet")
	if !allowed || !known {
		t.Errorf("golangci alias: allowed=%v known=%v, want true/true", allowed, known)
	}
}

func TestIsAllowed_ESLint(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		rule    string
		allowed bool
	}{
		{"no-unused-vars", true},
		{"eqeqeq", true},
		{"prefer-const", true},
		{"nonexistent-rule", false},
	}
	for _, tt := range tests {
		allowed, known := v.IsAllowed("eslint", tt.rule)
		if allowed != tt.allowed {
			t.Errorf("eslint/%s: allowed=%v, want %v", tt.rule, allowed, tt.allowed)
		}
		if !known {
			t.Errorf("eslint/%s: known=false, want true", tt.rule)
		}
	}
}

func TestIsAllowed_ESLintAlias(t *testing.T) {
	v := NewValidator()
	allowed, known := v.IsAllowed("@eslint/eslint", "no-unused-vars")
	if !allowed || !known {
		t.Errorf("@eslint/eslint alias: allowed=%v known=%v, want true/true", allowed, known)
	}
}

func TestIsAllowed_ESLintPluginPrefixes(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		rule    string
		allowed bool
	}{
		{"@typescript-eslint/no-explicit-any", true},
		{"react/jsx-key", true},
		{"react-hooks/exhaustive-deps", true},
		{"import/no-unresolved", true},
		{"unknown-prefix/some-rule", false},
	}
	for _, tt := range tests {
		allowed, known := v.IsAllowed("eslint", tt.rule)
		if allowed != tt.allowed {
			t.Errorf("eslint/%s: allowed=%v, want %v", tt.rule, allowed, tt.allowed)
		}
		if !known {
			t.Errorf("eslint/%s: known=false, want true", tt.rule)
		}
	}
}

func TestIsAllowed_Ruff(t *testing.T) {
	v := NewValidator()

	tests := []struct {
		rule    string
		allowed bool
	}{
		{"F401", true},
		{"E501", true},
		{"W503", true},
		{"RUF001", true},
		{"I001", true},
		{"C901", true},
		{"C902", true},
		{"PLR2004", true},
		{"XXX999", false},
		{"not-a-code", false},
	}
	for _, tt := range tests {
		allowed, known := v.IsAllowed("ruff", tt.rule)
		if allowed != tt.allowed {
			t.Errorf("ruff/%s: allowed=%v, want %v", tt.rule, allowed, tt.allowed)
		}
		if !known {
			t.Errorf("ruff/%s: known=false, want true", tt.rule)
		}
	}
}

func TestIsAllowed_UnknownTool(t *testing.T) {
	v := NewValidator()
	allowed, known := v.IsAllowed("some-other-tool", "some-rule")
	if allowed {
		t.Error("unknown tool should not be allowed")
	}
	if known {
		t.Error("unknown tool should not be known")
	}
}

func TestIsAllowed_EmptyInputs(t *testing.T) {
	v := NewValidator()

	allowed, known := v.IsAllowed("", "rule")
	if allowed || known {
		t.Error("empty tool should return false/false")
	}

	allowed, known = v.IsAllowed("eslint", "")
	if allowed || known {
		t.Errorf("empty rule: allowed=%v known=%v, want false/false", allowed, known)
	}

	allowed, known = v.IsAllowed("  ", "  ")
	if allowed || known {
		t.Error("whitespace-only should return false/false")
	}
}

func TestIsAllowed_CaseInsensitive(t *testing.T) {
	v := NewValidator()
	// Tool names should be case-insensitive
	allowed, known := v.IsAllowed("GolangCI-Lint", "govet")
	if !allowed || !known {
		t.Errorf("case insensitive tool: allowed=%v known=%v", allowed, known)
	}
}

func TestRuffMatches(t *testing.T) {
	tests := []struct {
		rule string
		want bool
	}{
		{"F401", true},
		{"E501", true},
		{"PLR2004", true},
		{"RUF001", true},
		{"I001", true},
		{"C901", true},   // C90 prefix + digit
		{"C902", true},   // C90 prefix + digits
		{"RUF100", true}, // RUF prefix + digits
		{"W503", true},
		{"", false},
		{"ABC", false},
		{"123", false},
		{"F", false},    // prefix only, no digits
		{"FABC", false}, // prefix + non-digits
		{"C90", false},  // prefix only, no trailing digits
		{"XXX999", false},
		{"not-a-code", false},
	}
	for _, tt := range tests {
		got := ruffMatches(tt.rule)
		if got != tt.want {
			t.Errorf("ruffMatches(%q) = %v, want %v", tt.rule, got, tt.want)
		}
	}
}
