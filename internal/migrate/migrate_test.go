package migrate

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestRunNoopWhenCurrentVersion(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
		Migrations: nil,
	}
	data := []byte(`{"version":2,"name":"hello"}`)
	out, result, err := reg.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Migrated() {
		t.Fatal("expected no migration")
	}
	if result.FromVersion != 2 || result.ToVersion != 2 {
		t.Fatalf("versions = (%d, %d), want (2, 2)", result.FromVersion, result.ToVersion)
	}
	if string(out) != string(data) {
		t.Fatal("expected data unchanged")
	}
}

func TestRunRejectsFutureVersion(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
	}
	data := []byte(`{"version":5}`)
	_, _, err := reg.Run(data)
	if err == nil {
		t.Fatal("expected error for future version")
	}
}

func TestRunRejectsExplicitZero(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
	}
	data := []byte(`{"version":0}`)
	_, _, err := reg.Run(data)
	if err == nil {
		t.Fatal("expected error for explicit version=0")
	}
}

func TestRunSingleStepMigration(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
		Migrations: []Migration{
			{
				FromVersion: 1,
				ToVersion:   2,
				Description: "add name field",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 2)
				},
			},
		},
	}
	data := []byte(`{"version":1}`)
	out, result, err := reg.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Migrated() {
		t.Fatal("expected migration")
	}
	if result.StepsApplied != 1 {
		t.Fatalf("StepsApplied = %d, want 1", result.StepsApplied)
	}
	ver, ok, _ := PeekVersion(out)
	if !ok || ver != 2 {
		t.Fatalf("output version = %d (exists=%v), want 2", ver, ok)
	}
}

func TestRunMultiStepMigration(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 3,
		Migrations: []Migration{
			{
				FromVersion: 1,
				ToVersion:   2,
				Description: "v1 to v2",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 2)
				},
			},
			{
				FromVersion: 2,
				ToVersion:   3,
				Description: "v2 to v3",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 3)
				},
			},
		},
	}
	data := []byte(`{"version":1}`)
	out, result, err := reg.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.StepsApplied != 2 {
		t.Fatalf("StepsApplied = %d, want 2", result.StepsApplied)
	}
	ver, _, _ := PeekVersion(out)
	if ver != 3 {
		t.Fatalf("output version = %d, want 3", ver)
	}
}

func TestRunIncompleteMigrationChain(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 3,
		Migrations: []Migration{
			{
				FromVersion: 1,
				ToVersion:   2,
				Description: "v1 to v2",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 2)
				},
			},
			// Missing v2→v3
		},
	}
	data := []byte(`{"version":1}`)
	_, _, err := reg.Run(data)
	if err == nil {
		t.Fatal("expected error for incomplete chain")
	}
}

func TestRunLegacyFileNoVersion(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 1,
		Migrations: []Migration{
			{
				FromVersion: 0,
				ToVersion:   1,
				Description: "add version field",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 1)
				},
			},
		},
	}
	data := []byte(`{"name":"hello"}`)
	out, result, err := reg.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Migrated() {
		t.Fatal("expected migration from legacy")
	}
	if result.FromVersion != 0 {
		t.Fatalf("FromVersion = %d, want 0", result.FromVersion)
	}
	ver, ok, _ := PeekVersion(out)
	if !ok || ver != 1 {
		t.Fatalf("output version = %d (exists=%v), want 1", ver, ok)
	}
}

func TestRunMigrationError(t *testing.T) {
	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
		Migrations: []Migration{
			{
				FromVersion: 1,
				ToVersion:   2,
				Description: "failing migration",
				Migrate: func(data []byte) ([]byte, error) {
					return nil, fmt.Errorf("intentional failure")
				},
			},
		},
	}
	data := []byte(`{"version":1}`)
	_, _, err := reg.Run(data)
	if err == nil {
		t.Fatal("expected error from failing migration")
	}
}

func TestRunPreservesDataDuringMigration(t *testing.T) {
	type payload struct {
		Version int    `json:"version"`
		Name    string `json:"name"`
		Count   int    `json:"count"`
	}

	reg := Registry{
		Name:       "test.json",
		CurrentVer: 2,
		Migrations: []Migration{
			{
				FromVersion: 1,
				ToVersion:   2,
				Description: "bump version only",
				Migrate: func(data []byte) ([]byte, error) {
					return SetVersion(data, 2)
				},
			},
		},
	}
	orig := payload{Version: 1, Name: "alice", Count: 42}
	data, _ := json.Marshal(orig)

	out, result, err := reg.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Migrated() {
		t.Fatal("expected migration")
	}

	var got payload
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got.Name != "alice" || got.Count != 42 || got.Version != 2 {
		t.Fatalf("data not preserved: got %+v", got)
	}
}

func TestPeekVersion(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantVer int
		wantOK  bool
		wantErr bool
	}{
		{"present", `{"version":3}`, 3, true, false},
		{"missing", `{"name":"x"}`, 0, false, false},
		{"null", `{"version":null}`, 0, true, false},
		{"string", `{"version":"two"}`, 0, false, true},
		{"empty object", `{}`, 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, ok, err := PeekVersion([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Fatalf("PeekVersion error = %v, wantErr %v", err, tt.wantErr)
			}
			if ver != tt.wantVer || ok != tt.wantOK {
				t.Fatalf("PeekVersion = (%d, %v), want (%d, %v)", ver, ok, tt.wantVer, tt.wantOK)
			}
		})
	}
}

func TestSetVersion(t *testing.T) {
	data := []byte(`{"version":1,"name":"x"}`)
	out, err := SetVersion(data, 5)
	if err != nil {
		t.Fatalf("SetVersion: %v", err)
	}
	ver, ok, _ := PeekVersion(out)
	if !ok || ver != 5 {
		t.Fatalf("version = %d (exists=%v), want 5", ver, ok)
	}
	// Verify other fields preserved.
	var m map[string]any
	json.Unmarshal(out, &m)
	if m["name"] != "x" {
		t.Fatalf("name = %v, want x", m["name"])
	}
}
