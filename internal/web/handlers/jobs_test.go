package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"dreamer/internal/backgroundjobs"
	"dreamer/internal/config"
	"dreamer/internal/logging"
)

// --- Mock stores ---

type mockJobStore struct {
	state *backgroundjobs.State
	err   error
}

func (m *mockJobStore) Load() (*backgroundjobs.State, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.state, nil
}

func (m *mockJobStore) Update(ctx context.Context, mutate func(*backgroundjobs.State) error) error {
	if m.err != nil {
		return m.err
	}
	return mutate(m.state)
}

type mockRunStore struct {
	runs []backgroundjobs.Run
	err  error
}

func (m *mockRunStore) List(jobID string) ([]backgroundjobs.Run, error) {
	if m.err != nil {
		return nil, m.err
	}
	var out []backgroundjobs.Run
	for _, r := range m.runs {
		if r.JobID == jobID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *mockRunStore) Latest(jobID string) (*backgroundjobs.Run, error) {
	if m.err != nil {
		return nil, m.err
	}
	for _, r := range m.runs {
		if r.JobID == jobID {
			return &r, nil
		}
	}
	return nil, nil
}

func (m *mockRunStore) Count(jobID string) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	count := 0
	for _, r := range m.runs {
		if r.JobID == jobID {
			count++
		}
	}
	return count, nil
}

func (m *mockRunStore) Dir() string { return "" }

type mockAuditWriter struct {
	events    []backgroundjobs.AuditEvent
	readError error // if set, ReadAll returns this error
}

func (m *mockAuditWriter) Write(event backgroundjobs.AuditEvent) error {
	m.events = append(m.events, event)
	return nil
}

func (m *mockAuditWriter) ReadAll(limit int) ([]backgroundjobs.AuditEvent, error) {
	if m.readError != nil {
		return nil, m.readError
	}
	events := make([]backgroundjobs.AuditEvent, len(m.events))
	copy(events, m.events)
	// Match real implementation: sort newest-first.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})
	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

type mockExecutor struct {
	result backgroundjobs.RunResult
	err    error
}

func (m *mockExecutor) Run(ctx context.Context, jobID string) (backgroundjobs.RunResult, error) {
	return m.result, m.err
}

// --- Helpers ---

func testConfig() *config.App {
	return &config.App{
		Projects: []config.ProjectConfig{
			{Name: "proj-a", Path: "/tmp/proj-a"},
		},
		Providers: map[string]config.ProviderBlock{
			"claude-cli": {Model: "claude-sonnet-4-20250514"},
		},
	}
}

func testLogger() *logging.Logger {
	return logging.Silent()
}

func mockProviderLookup(id string) *backgroundjobs.ProviderMeta {
	safe := map[string]bool{
		"claude-cli":     true,
		"codex-cli":      true,
		"gemini-cli":     true,
		"openclaude-cli": true,
	}
	if safe[id] {
		return &backgroundjobs.ProviderMeta{
			ID:             id,
			DisplayName:    id,
			BackgroundSafe: true,
		}
	}
	return nil
}

func makeJob(id, name string, enabled bool) *backgroundjobs.Job {
	return &backgroundjobs.Job{
		ID:         id,
		Name:       name,
		Prompt:     "test prompt",
		ProviderID: "claude-cli",
		Enabled:    enabled,
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
	}
}

func jsonBody(v any) *bytes.Buffer {
	buf, _ := json.Marshal(v)
	return bytes.NewBuffer(buf)
}

// --- Tests ---

func TestJobList_ReturnsJobs(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store:               store,
			Runs:                &mockRunStore{},
			ResolveProviderMeta: mockProviderLookup,
		},
	}

	r := httptest.NewRequest("GET", "/api/jobs", nil)
	w := httptest.NewRecorder()
	JobList(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	jobs := resp["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
}

func TestJobList_NilStoreReturns503(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("GET", "/api/jobs", nil)
	w := httptest.NewRecorder()
	JobList(deps)(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestJobCreate_HappyPath(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}}}
	audit := &mockAuditWriter{}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store:               store,
			Runs:                &mockRunStore{},
			Audit:               audit,
			ResolveProviderMeta: mockProviderLookup,
		},
	}

	body := createPayload{
		Prompt:      "analyze code",
		ProjectName: "proj-a",
		ProviderID:  "claude-cli",
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
	}
	r := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w := httptest.NewRecorder()
	JobCreate(deps)(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["job_id"] == nil || resp["job_id"] == "" {
		t.Fatal("expected job_id in response")
	}
	if len(audit.events) != 1 || audit.events[0].Event != "job.create" {
		t.Fatalf("expected 1 audit event, got %d", len(audit.events))
	}
}

func TestJobCreate_EmptyPromptReturns400(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: &mockJobStore{state: &backgroundjobs.State{}}, ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:     "",
		ProviderID: "claude-cli",
		Schedule:   backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval},
	}
	r := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w := httptest.NewRecorder()
	JobCreate(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestJobCreate_InvalidScheduleReturns400(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: &mockJobStore{state: &backgroundjobs.State{}}, ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:     "do something",
		ProviderID: "claude-cli",
		Schedule:   backgroundjobs.ScheduleSpec{Kind: "invalid"},
	}
	r := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w := httptest.NewRecorder()
	JobCreate(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestJobCreate_UnknownProviderReturns400(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: &mockJobStore{state: &backgroundjobs.State{}}, ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:     "do something",
		ProviderID: "nonexistent",
		Schedule:   backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval},
	}
	r := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w := httptest.NewRecorder()
	JobCreate(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestJobCreate_JobLimitReachedReturns400(t *testing.T) {
	jobs := make(map[string]*backgroundjobs.Job)
	for i := 0; i < maxJobsPerInstall; i++ {
		id := fmt.Sprintf("job%013d", i)
		jobs[id] = makeJob(id, "filler", true)
	}
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: jobs}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:      "do something",
		ProjectName: "proj-a",
		ProviderID:  "claude-cli",
		Schedule:    backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval, Timezone: "UTC"},
	}
	r := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w := httptest.NewRecorder()
	JobCreate(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "128") {
		t.Fatalf("expected error about 128 limit, got %s", w.Body.String())
	}
}

func TestJobPreview_HappyPath(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:      "analyze code",
		ProjectName: "proj-a",
		ProviderID:  "claude-cli",
		Schedule: backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleDaily,
			TimeOfDay: "09:00",
			Timezone:  "UTC",
		},
	}
	r := httptest.NewRequest("POST", "/api/jobs/preview", jsonBody(body))
	w := httptest.NewRecorder()
	JobPreview(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["valid"] != true {
		t.Fatal("expected valid=true")
	}
	if resp["schedule_summary"] == nil {
		t.Fatal("expected schedule_summary")
	}
}

func TestJobDetail_HappyPath(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store:               store,
			Runs:                &mockRunStore{},
			ResolveProviderMeta: mockProviderLookup,
		},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890", nil)
	w := httptest.NewRecorder()
	JobDetail(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["job"] == nil {
		t.Fatal("expected job in response")
	}
}

func TestJobDetail_NotFoundReturns404(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890", nil)
	w := httptest.NewRecorder()
	JobDetail(deps)(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestJobDelete_HappyPath(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	audit := &mockAuditWriter{}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, Audit: audit},
	}

	r := httptest.NewRequest("DELETE", "/api/jobs/abc123def4567890", nil)
	w := httptest.NewRecorder()
	JobDelete(deps)(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if store.state.Jobs["abc123def4567890"] != nil {
		t.Fatal("expected job to be deleted")
	}
	if len(audit.events) != 1 || audit.events[0].Event != "job.delete" {
		t.Fatalf("expected audit event, got %d events", len(audit.events))
	}
}

func TestJobRunNow_HappyPath(t *testing.T) {
	runID := "run123"
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	executor := &mockExecutor{
		result: backgroundjobs.RunResult{
			Record: backgroundjobs.Run{
				ID:             runID,
				Status:         backgroundjobs.RunStatusCompleted,
				DurationMillis: 1500,
			},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, Executor: executor, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/run", nil)
	w := httptest.NewRecorder()
	JobRunNow(deps)(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["job_id"] != "abc123def4567890" {
		t.Fatalf("expected job_id=abc123def4567890, got %v", resp["job_id"])
	}
	if resp["status"] != "running" {
		t.Fatalf("expected status=running, got %v", resp["status"])
	}
	// Allow background goroutine to complete.
	time.Sleep(50 * time.Millisecond)
}

func TestJobRunNow_ConflictReturns409(t *testing.T) {
	// Acquire the run guard to simulate an in-progress run.
	jobID := "abc123def4567890"
	if !tryAcquireRun(jobID) {
		t.Fatal("expected to acquire run guard")
	}
	defer releaseRun(jobID)

	job := makeJob(jobID, "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{jobID: job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, Executor: &mockExecutor{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/"+jobID+"/run", nil)
	w := httptest.NewRecorder()
	JobRunNow(deps)(w, r)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestJobSetEnabled_Pause(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/pause", nil)
	w := httptest.NewRecorder()
	JobSetEnabled(deps, false)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if job.Enabled {
		t.Fatal("expected job to be disabled after pause")
	}
}

func TestJobSetEnabled_Resume(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", false)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/resume", nil)
	w := httptest.NewRecorder()
	JobSetEnabled(deps, true)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if !job.Enabled {
		t.Fatal("expected job to be enabled after resume")
	}
}

func TestJobRuns_Pagination(t *testing.T) {
	runs := make([]backgroundjobs.Run, 100)
	for i := 0; i < 100; i++ {
		runs[i] = backgroundjobs.Run{
			ID:     fmt.Sprintf("run%04d", i),
			JobID:  "abc123def4567890",
			Status: backgroundjobs.RunStatusCompleted,
		}
	}
	store := &mockJobStore{state: &backgroundjobs.State{
		Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": makeJob("abc123def4567890", "test", true)},
	}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store: store,
			Runs:  &mockRunStore{runs: runs},
		},
	}

	// Default limit (50).
	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs", nil)
	w := httptest.NewRecorder()
	JobRuns(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if int(resp["total"].(float64)) != 100 {
		t.Fatalf("expected total=100, got %v", resp["total"])
	}
	runsArr := resp["runs"].([]any)
	if len(runsArr) != 50 {
		t.Fatalf("expected 50 runs (default limit), got %d", len(runsArr))
	}

	// Custom limit.
	r2 := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs?limit=10", nil)
	w2 := httptest.NewRecorder()
	JobRuns(deps)(w2, r2)

	var resp2 map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	runsArr2 := resp2["runs"].([]any)
	if len(runsArr2) != 10 {
		t.Fatalf("expected 10 runs, got %d", len(runsArr2))
	}
}

func TestJobRunDetail_HappyPath(t *testing.T) {
	run := backgroundjobs.Run{
		ID:             "run123",
		JobID:          "abc123def4567890",
		Status:         backgroundjobs.RunStatusCompleted,
		DurationMillis: 500,
	}
	store := &mockJobStore{state: &backgroundjobs.State{
		Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": makeJob("abc123def4567890", "test", true)},
	}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store: store,
			Runs:  &mockRunStore{runs: []backgroundjobs.Run{run}},
		},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs/run123", nil)
	w := httptest.NewRecorder()
	JobRunDetail(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var resp backgroundjobs.Run
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != "run123" {
		t.Fatalf("expected run ID run123, got %s", resp.ID)
	}
}

func TestJobRunDetail_NotFoundReturns404(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{
		Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": makeJob("abc123def4567890", "test", true)},
	}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs: JobDeps{
			Store:               store,
			Runs:                &mockRunStore{},
			ResolveProviderMeta: mockProviderLookup,
		},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs/nonexistent", nil)
	w := httptest.NewRecorder()
	JobRunDetail(deps)(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// --- Route dispatcher tests ---

func TestRouteJobs_DispatchToDetail(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_CollectionEndpoint(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	// GET /api/jobs -> list
	r := httptest.NewRequest("GET", "/api/jobs", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/jobs: expected 200, got %d", w.Code)
	}

	// POST /api/jobs -> create
	body := createPayload{
		Prompt:      "test",
		ProjectName: "proj-a",
		ProviderID:  "claude-cli",
		Schedule:    backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval, Timezone: "UTC"},
	}
	r2 := httptest.NewRequest("POST", "/api/jobs", jsonBody(body))
	w2 := httptest.NewRecorder()
	RouteJobs(deps)(w2, r2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("POST /api/jobs: expected 201, got %d body=%s", w2.Code, w2.Body.String())
	}

	// PUT /api/jobs -> 405
	r3 := httptest.NewRequest("PUT", "/api/jobs", nil)
	w3 := httptest.NewRecorder()
	RouteJobs(deps)(w3, r3)
	if w3.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/jobs: expected 405, got %d", w3.Code)
	}
}

func TestRouteJobs_InvalidJobIDReturns400(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: &mockJobStore{state: &backgroundjobs.State{}}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("GET", "/api/jobs/invalid!!!", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRouteJobs_DispatchToRun(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	executor := &mockExecutor{
		result: backgroundjobs.RunResult{
			Record: backgroundjobs.Run{ID: "run1", Status: backgroundjobs.RunStatusCompleted},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, Executor: executor, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/run", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_DispatchToPause(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/pause", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if job.Enabled {
		t.Fatal("expected job to be paused")
	}
}

func TestRouteJobs_DispatchToResume(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", false)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("POST", "/api/jobs/abc123def4567890/resume", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !job.Enabled {
		t.Fatal("expected job to be resumed")
	}
}

func TestRouteJobs_DispatchToRuns(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{
		Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": makeJob("abc123def4567890", "test", true)},
	}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_DispatchToRunDetail(t *testing.T) {
	run := backgroundjobs.Run{ID: "run123", JobID: "abc123def4567890", Status: backgroundjobs.RunStatusCompleted}
	store := &mockJobStore{state: &backgroundjobs.State{
		Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": makeJob("abc123def4567890", "test", true)},
	}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{runs: []backgroundjobs.Run{run}}},
	}

	r := httptest.NewRequest("GET", "/api/jobs/abc123def4567890/runs/run123", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_DispatchToDelete(t *testing.T) {
	job := makeJob("abc123def4567890", "test job", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	r := httptest.NewRequest("DELETE", "/api/jobs/abc123def4567890", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_DispatchToPreview(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{ResolveProviderMeta: mockProviderLookup},
	}

	body := createPayload{
		Prompt:      "test",
		ProjectName: "proj-a",
		ProviderID:  "claude-cli",
		Schedule:    backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval, Timezone: "UTC"},
	}
	r := httptest.NewRequest("POST", "/api/jobs/preview", jsonBody(body))
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_DispatchToHealth(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}},
	}

	r := httptest.NewRequest("GET", "/api/jobs/health", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouteJobs_EmptyPathHandlesMethodDispatch(t *testing.T) {
	store := &mockJobStore{state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}}}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	// /api/jobs/ with trailing slash should dispatch to list (GET) or create (POST).
	r := httptest.NewRequest("GET", "/api/jobs/", nil)
	w := httptest.NewRecorder()
	RouteJobs(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

// --- Helper function tests ---

func TestDeriveNameFromPrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{"first line", "analyze code\nand fix bugs", "analyze code"},
		{"single line", "short prompt", "short prompt"},
		{"empty", "", "background job"},
		{"whitespace only", "   \n  ", "background job"},
		{"truncation", strings.Repeat("a", 100), strings.Repeat("a", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveNameFromPrompt(tt.prompt)
			if got != tt.want {
				t.Fatalf("deriveNameFromPrompt(%q) = %q, want %q", tt.prompt, got, tt.want)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "hello world"},
		{"line1\nline2", "line1 line2"},
		{"tab\there", "tab here"},
		{"control\x01char", "controlchar"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildScheduleSummary(t *testing.T) {
	tests := []struct {
		name string
		spec backgroundjobs.ScheduleSpec
		want string
	}{
		{"hourly", backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleInterval}, "Every hour"},
		{"daily", backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleDaily, TimeOfDay: "09:00", Timezone: "UTC"}, "Daily at 09:00 UTC"},
		{"weekly", backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleWeekly, DayOfWeek: "monday", TimeOfDay: "10:00", Timezone: "US/Eastern"}, "Weekly on monday at 10:00 US/Eastern"},
		{"cron", backgroundjobs.ScheduleSpec{Kind: backgroundjobs.ScheduleCron, Cron: "*/5 * * * *", Timezone: "UTC"}, "Cron: */5 * * * * (UTC)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildScheduleSummary(tt.spec)
			if got != tt.want {
				t.Fatalf("buildScheduleSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractJobID(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/api/jobs/abc123def4567890", "abc123def4567890"},
		{"/api/jobs/abc123def4567890/run", "abc123def4567890"},
		{"/api/jobs/", ""},
		{"/api/jobs", ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := extractJobID(tt.path)
			if got != tt.want {
				t.Fatalf("extractJobID(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractJobIDAndRunID(t *testing.T) {
	jid, rid := extractJobIDAndRunID("/api/jobs/abc123def4567890/runs/run123")
	if jid != "abc123def4567890" || rid != "run123" {
		t.Fatalf("got (%q, %q), want (abc123def4567890, run123)", jid, rid)
	}
}

// --- ResolveProjectPath tests ---

func TestResolveProjectPath_KnownProject(t *testing.T) {
	cfg := testConfig()
	path, err := resolveProjectPath(cfg, "proj-a")
	if err != nil {
		t.Fatal(err)
	}
	if path != cfg.Projects[0].Path {
		t.Fatalf("expected %s, got %s", cfg.Projects[0].Path, path)
	}
}

func TestResolveProjectPath_UnknownProject(t *testing.T) {
	cfg := testConfig()
	_, err := resolveProjectPath(cfg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestResolveProjectPath_EmptyUsesFirst(t *testing.T) {
	cfg := testConfig()
	path, err := resolveProjectPath(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if path != cfg.Projects[0].Path {
		t.Fatalf("expected %s, got %s", cfg.Projects[0].Path, path)
	}
}

// TestResolveProjectPath_EmptyMultipleIsAmbiguous ensures an empty project_name
// is rejected when several projects exist, rather than silently targeting the
// first — which would run the job against an unintended project.
func TestResolveProjectPath_EmptyMultipleIsAmbiguous(t *testing.T) {
	cfg := testConfig()
	cfg.Projects = append(cfg.Projects, config.ProjectConfig{Name: "proj-b", Path: "/tmp/proj-b"})
	_, err := resolveProjectPath(cfg, "")
	if err == nil {
		t.Fatal("expected error for empty project_name with multiple projects")
	}
	if !strings.Contains(err.Error(), "project_name is required") {
		t.Fatalf("expected 'project_name is required' error, got: %v", err)
	}
}

func TestResolveProjectPath_EmptyNoProjects(t *testing.T) {
	cfg := testConfig()
	cfg.Projects = nil
	_, err := resolveProjectPath(cfg, "")
	if err == nil {
		t.Fatal("expected error when no projects configured")
	}
}

func TestJobAuditLog_ReturnsEvents(t *testing.T) {
	audit := &mockAuditWriter{
		events: []backgroundjobs.AuditEvent{
			{Timestamp: time.Now().Add(-time.Minute), Event: "job.create", JobID: "abc"},
			{Timestamp: time.Now(), Event: "job.run.finish", JobID: "abc"},
		},
	}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}

	r := httptest.NewRequest("GET", "/api/jobs/audit", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Events []backgroundjobs.AuditEvent `json:"events"`
		Total  int                         `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 2 {
		t.Errorf("total = %d, want 2", resp.Total)
	}
	if len(resp.Events) != 2 {
		t.Errorf("events count = %d, want 2", len(resp.Events))
	}
}

func TestJobAuditLog_RespectsLimit(t *testing.T) {
	events := make([]backgroundjobs.AuditEvent, 10)
	for i := range events {
		events[i] = backgroundjobs.AuditEvent{Timestamp: time.Now(), Event: "test"}
	}
	audit := &mockAuditWriter{events: events}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}

	r := httptest.NewRequest("GET", "/api/jobs/audit?limit=3", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Events []backgroundjobs.AuditEvent `json:"events"`
		Total  int                         `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 3 {
		t.Errorf("events count = %d, want 3 (limited)", len(resp.Events))
	}
	if resp.Total != 3 {
		t.Errorf("total = %d, want 3", resp.Total)
	}
}

func TestJobAuditLog_NilAudit(t *testing.T) {
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: nil},
		Logger: testLogger(),
	}
	r := httptest.NewRequest("GET", "/api/jobs/audit", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestJobAuditLog_InvalidLimitFallsBack(t *testing.T) {
	events := make([]backgroundjobs.AuditEvent, 5)
	for i := range events {
		events[i] = backgroundjobs.AuditEvent{Event: "test"}
	}
	audit := &mockAuditWriter{events: events}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}
	r := httptest.NewRequest("GET", "/api/jobs/audit?limit=abc", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Invalid limit falls back to default 100; with 5 events, all returned.
	if int(resp["total"].(float64)) != 5 {
		t.Errorf("total = %v, want 5 (default limit)", resp["total"])
	}
}

func TestJobAuditLog_LimitOver500Capped(t *testing.T) {
	events := make([]backgroundjobs.AuditEvent, 600)
	for i := range events {
		events[i] = backgroundjobs.AuditEvent{Event: "test"}
	}
	audit := &mockAuditWriter{events: events}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}
	r := httptest.NewRequest("GET", "/api/jobs/audit?limit=1000", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Handler caps at 500; mock returns all 600, handler should truncate.
	if int(resp["total"].(float64)) != 500 {
		t.Errorf("total = %v, want 500 (capped)", resp["total"])
	}
}

func TestJobAuditLog_ReadAllError(t *testing.T) {
	audit := &mockAuditWriter{readError: fmt.Errorf("disk error")}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}
	r := httptest.NewRequest("GET", "/api/jobs/audit", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestJobAuditLog_MockSortsNewestFirst(t *testing.T) {
	// Insert events in chronological order — mock should sort newest-first.
	audit := &mockAuditWriter{
		events: []backgroundjobs.AuditEvent{
			{Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), Event: "old"},
			{Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), Event: "new"},
			{Timestamp: time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), Event: "mid"},
		},
	}
	deps := Deps{
		Config: testConfig,
		Jobs:   JobDeps{Audit: audit},
		Logger: testLogger(),
	}

	r := httptest.NewRequest("GET", "/api/jobs/audit", nil)
	w := httptest.NewRecorder()
	JobAuditLog(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Events []backgroundjobs.AuditEvent `json:"events"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 3 {
		t.Fatalf("events count = %d, want 3", len(resp.Events))
	}
	if resp.Events[0].Event != "new" {
		t.Errorf("first event = %q, want 'new' (newest first)", resp.Events[0].Event)
	}
	if resp.Events[2].Event != "old" {
		t.Errorf("last event = %q, want 'old' (oldest last)", resp.Events[2].Event)
	}
}

func TestJobEdit_HappyPath(t *testing.T) {
	job := makeJob("abc123def4567890", "old name", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{"name": "new name", "prompt": "new prompt"})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if job.Name != "new name" {
		t.Errorf("name = %q, want %q", job.Name, "new name")
	}
	if job.Prompt != "new prompt" {
		t.Errorf("prompt = %q, want %q", job.Prompt, "new prompt")
	}
}

func TestJobEdit_ScheduleChange(t *testing.T) {
	job := makeJob("abc123def4567890", "test", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{
		"schedule": backgroundjobs.ScheduleSpec{
			Kind:      backgroundjobs.ScheduleWeekly,
			DayOfWeek: "monday",
			TimeOfDay: "10:00",
			Timezone:  "UTC",
		},
	})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if job.Schedule.Kind != backgroundjobs.ScheduleWeekly {
		t.Errorf("schedule kind = %q, want %q", job.Schedule.Kind, backgroundjobs.ScheduleWeekly)
	}
	if job.NextRunAt == nil {
		t.Error("expected NextRunAt to be recalculated")
	}
}

func TestJobEdit_NotFoundReturns404(t *testing.T) {
	store := &mockJobStore{
		state: &backgroundjobs.State{Jobs: map[string]*backgroundjobs.Job{}},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{"name": "new"})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", w.Code)
	}
}

func TestJobEdit_EmptyBodyReturns400(t *testing.T) {
	job := makeJob("abc123def4567890", "test", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestJobEdit_InvalidScheduleReturns400(t *testing.T) {
	job := makeJob("abc123def4567890", "test", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{
		"schedule": backgroundjobs.ScheduleSpec{
			Kind:     backgroundjobs.ScheduleDaily,
			Timezone: "UTC",
			// Missing TimeOfDay.
		},
	})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestJobEdit_UnknownProviderReturns400(t *testing.T) {
	job := makeJob("abc123def4567890", "test", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	body := jsonBody(map[string]any{"provider_id": "nonexistent"})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestJobEdit_PromptTooLongReturns400(t *testing.T) {
	job := makeJob("abc123def4567890", "test", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}

	longPrompt := strings.Repeat("x", 17*1024)
	body := jsonBody(map[string]any{"prompt": longPrompt})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	JobEdit(deps)(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", w.Code)
	}
}

func TestRouteJobs_DispatchToPatch(t *testing.T) {
	job := makeJob("abc123def4567890", "old", true)
	store := &mockJobStore{
		state: &backgroundjobs.State{
			Jobs: map[string]*backgroundjobs.Job{"abc123def4567890": job},
		},
	}
	deps := Deps{
		Config: testConfig,
		Logger: testLogger(),
		Jobs:   JobDeps{Store: store, Runs: &mockRunStore{}, ResolveProviderMeta: mockProviderLookup},
	}
	handler := RouteJobs(deps)

	body := jsonBody(map[string]any{"name": "patched"})
	r := httptest.NewRequest("PATCH", "/api/jobs/abc123def4567890", body)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	if job.Name != "patched" {
		t.Errorf("name = %q, want %q", job.Name, "patched")
	}
}
