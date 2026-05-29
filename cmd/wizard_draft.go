package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dreamer/internal/fsutil"
)

// jobWizardAnswers collects all values the wizard gathers.
type jobWizardAnswers struct {
	projectPath   string
	name          string
	providerID    string
	model         string
	prompt        string
	fileAccess    string // read_only, selected_writes, full_workspace
	writablePaths string // comma-separated, for selected_writes (CLI only)
	scheduleKind  string
	every         string
	timeOfDay     string
	dayOfWeek     string
	cron          string
	timezone      string
}

// jobWizardAnswersJSON is the exported-field mirror used for JSON
// serialization of jobWizardAnswers. The fields are kept in sync with
// the unexported struct manually.
type jobWizardAnswersJSON struct {
	ProjectPath   string `json:"project_path,omitempty"`
	Name          string `json:"name,omitempty"`
	ProviderID    string `json:"provider_id,omitempty"`
	Model         string `json:"model,omitempty"`
	Prompt        string `json:"prompt,omitempty"`
	FileAccess    string `json:"file_access,omitempty"`
	WritablePaths string `json:"writable_paths,omitempty"`
	ScheduleKind  string `json:"schedule_kind,omitempty"`
	Every         string `json:"every,omitempty"`
	TimeOfDay     string `json:"time_of_day,omitempty"`
	DayOfWeek     string `json:"day_of_week,omitempty"`
	Cron          string `json:"cron,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
}

func (a jobWizardAnswers) MarshalJSON() ([]byte, error) {
	return json.Marshal(jobWizardAnswersJSON{
		ProjectPath:   a.projectPath,
		Name:          a.name,
		ProviderID:    a.providerID,
		Model:         a.model,
		Prompt:        a.prompt,
		FileAccess:    a.fileAccess,
		WritablePaths: a.writablePaths,
		ScheduleKind:  a.scheduleKind,
		Every:         a.every,
		TimeOfDay:     a.timeOfDay,
		DayOfWeek:     a.dayOfWeek,
		Cron:          a.cron,
		Timezone:      a.timezone,
	})
}

func (a *jobWizardAnswers) UnmarshalJSON(data []byte) error {
	var raw jobWizardAnswersJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.projectPath = raw.ProjectPath
	a.name = raw.Name
	a.providerID = raw.ProviderID
	a.model = raw.Model
	a.prompt = raw.Prompt
	a.fileAccess = raw.FileAccess
	a.writablePaths = raw.WritablePaths
	a.scheduleKind = raw.ScheduleKind
	a.every = raw.Every
	a.timeOfDay = raw.TimeOfDay
	a.dayOfWeek = raw.DayOfWeek
	a.cron = raw.Cron
	a.timezone = raw.Timezone
	return nil
}

// wizardDraftPath returns the path to the wizard draft JSON file.
func wizardDraftPath(outputRoot string) string {
	return filepath.Join(outputRoot, "background-jobs", ".wizard-draft.json")
}

// wizardDraftEnvelope wraps the draft answers with a version field for
// future schema migration.
type wizardDraftEnvelope struct {
	Version int              `json:"version"`
	Answers jobWizardAnswers `json:"answers"`
}

// loadWizardDraft reads the draft from disk. Returns a zero-value struct
// (not an error) when the file is missing or corrupt — the wizard should
// always be able to start fresh.
func loadWizardDraft(path string) (jobWizardAnswers, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return jobWizardAnswers{}, nil
	}
	if err != nil {
		return jobWizardAnswers{}, fmt.Errorf("read wizard draft: %w", err)
	}
	var env wizardDraftEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		// Corrupt draft — treat as empty.
		fmt.Fprintf(os.Stderr, "warning: ignoring corrupt wizard draft (%v)\n", err)
		return jobWizardAnswers{}, nil
	}
	if env.Version != 1 {
		// Unknown version — treat as empty.
		return jobWizardAnswers{}, nil
	}
	return env.Answers, nil
}

// saveWizardDraft persists partial wizard answers to disk so they can be
// pre-filled on the next run.
func saveWizardDraft(path string, answers jobWizardAnswers) error {
	if err := os.MkdirAll(filepath.Dir(path), fsutil.DirPerms); err != nil {
		return fmt.Errorf("create draft dir: %w", err)
	}
	env := wizardDraftEnvelope{Version: 1, Answers: answers}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal draft: %w", err)
	}
	return fsutil.WriteFileAtomic(path, data, fsutil.FilePerms)
}

// deleteWizardDraft removes the draft file. Ignores "not found" errors.
func deleteWizardDraft(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// mergeWizardDraft applies CLI-provided flags on top of a loaded draft.
// For each non-zero field in cliOverrides, the draft value is replaced.
// The projectPath is always taken from cliOverrides (CWD or arg), never
// from the draft.
func mergeWizardDraft(draft, cliOverrides jobWizardAnswers) jobWizardAnswers {
	out := draft

	// Path is always from the current invocation.
	if cliOverrides.projectPath != "" {
		out.projectPath = cliOverrides.projectPath
	} else {
		out.projectPath = "" // never restore stale path
	}

	// CLI flags override draft values when explicitly set.
	if cliOverrides.name != "" {
		out.name = cliOverrides.name
	}
	if cliOverrides.providerID != "" {
		out.providerID = cliOverrides.providerID
	}
	if cliOverrides.model != "" {
		out.model = cliOverrides.model
	}
	if cliOverrides.prompt != "" {
		out.prompt = cliOverrides.prompt
	}
	if cliOverrides.fileAccess != "" {
		out.fileAccess = cliOverrides.fileAccess
	}
	if cliOverrides.writablePaths != "" {
		out.writablePaths = cliOverrides.writablePaths
	}
	if cliOverrides.scheduleKind != "" {
		out.scheduleKind = cliOverrides.scheduleKind
	}
	if cliOverrides.every != "" {
		out.every = cliOverrides.every
	}
	if cliOverrides.timeOfDay != "" {
		out.timeOfDay = cliOverrides.timeOfDay
	}
	if cliOverrides.dayOfWeek != "" {
		out.dayOfWeek = cliOverrides.dayOfWeek
	}
	if cliOverrides.cron != "" {
		out.cron = cliOverrides.cron
	}
	if cliOverrides.timezone != "" {
		out.timezone = cliOverrides.timezone
	}

	return out
}
