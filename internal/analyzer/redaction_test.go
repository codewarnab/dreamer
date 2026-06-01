package analyzer

import (
	"testing"
)

func TestEnvLineRedaction(t *testing.T) {
	r, err := NewRedactor(nil)
	if err != nil {
		t.Fatalf("NewRedactor: %v", err)
	}

	tests := []struct {
		name     string
		input    string
		wantHits int
	}{
		{"DATABASE_URL", "DATABASE_URL=postgres://user:pass@host/db", 1},
		{"REDIS_URL", "REDIS_URL=redis://:secret@localhost:6379", 1},
		{"MONGO_URI", "MONGO_URI=mongodb://user:pass@mongo:27017/app", 1},
		{"DSN", "DSN=host=db user=admin password=s3cret dbname=test", 1},
		{"CONNECTION_STRING", "CONNECTION_STRING=Server=sql;Database=x;User Id=sa;Password=p;", 1},
		{"POSTGRES_DSN", "POSTGRES_DSN=postgresql://user:pass@pg/db", 1},
		{"SECRET_TOKEN", "SECRET_TOKEN=abc123", 1},
		{"OPENAI_API_KEY", "OPENAI_API_KEY=sk-abc123", 1},
		{"benign PATH", "PATH=/usr/bin:/usr/local/bin", 0},
		{"benign HOME", "HOME=/home/user", 0},
		{"benign LANG", "LANG=en_US.UTF-8", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, result := r.Redact(tt.input)
			got := result.HitsByName["env-line"]
			if got != tt.wantHits {
				t.Errorf("env-line hits = %d, want %d (input: %s)", got, tt.wantHits, tt.input)
			}
		})
	}
}
