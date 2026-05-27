package backgroundjobs

import (
	"strings"
	"sync"
	"testing"
)

func TestGenerateJobIDFormat(t *testing.T) {
	id, err := GenerateJobID()
	if err != nil {
		t.Fatalf("GenerateJobID: %v", err)
	}
	if err := ValidateJobID(id); err != nil {
		t.Fatalf("generated ID %q fails validation: %v", id, err)
	}
}

func TestGenerateJobIDUniqueness(t *testing.T) {
	const n = 100
	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id, err := GenerateJobID()
		if err != nil {
			t.Fatalf("GenerateJobID iteration %d: %v", i, err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID generated: %q", id)
		}
		seen[id] = true
	}
}

func TestGenerateJobIDConcurrency(t *testing.T) {
	const goroutines = 20
	var wg sync.WaitGroup
	ids := make(chan string, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			id, err := GenerateJobID()
			if err != nil {
				t.Errorf("GenerateJobID: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[string]bool, goroutines)
	for id := range ids {
		if seen[id] {
			t.Fatalf("duplicate concurrent ID: %q", id)
		}
		seen[id] = true
	}
}

func TestValidateJobIDValid(t *testing.T) {
	cases := []string{
		"abcdef1234567890",
		"0000000000000000",
		"ffffffffffffffff",
	}
	for _, id := range cases {
		if err := ValidateJobID(id); err != nil {
			t.Errorf("ValidateJobID(%q): expected nil, got %v", id, err)
		}
	}
}

func TestValidateJobIDInvalid(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"", "must be 16 lowercase hex chars"},
		{"abc", "must be 16 lowercase hex chars"},
		{"ABCDEF1234567890", "must be 16 lowercase hex chars"},
		{"abcdefg123456789", "must be 16 lowercase hex chars"},
		{"abcdef12345678901", "must be 16 lowercase hex chars"}, // 17 chars
		{"preview", "reserved"},
		{"health", "reserved"},
		{"reconcile", "reserved"},
		{"options", "reserved"},
		{"templates", "reserved"},
	}
	for _, tc := range cases {
		err := ValidateJobID(tc.id)
		if err == nil {
			t.Errorf("ValidateJobID(%q): expected error containing %q, got nil", tc.id, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateJobID(%q): error %q does not contain %q", tc.id, err, tc.want)
		}
	}
}
