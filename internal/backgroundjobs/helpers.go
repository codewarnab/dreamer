package backgroundjobs

import (
	"context"
	"fmt"

	"dreamer/internal/analyzer"
)

// ProviderMeta is a subset of analyzer.ProviderMeta exposed for CLI use.
type ProviderMeta struct {
	ID             string
	DisplayName    string
	BackgroundSafe bool
}

// ProviderMetaByID returns provider metadata for the given ID, or nil if
// the provider is not registered. Looks up from the analyzer registry.
func ProviderMetaByID(id string) *ProviderMeta {
	for _, m := range analyzer.RegisteredProviderMeta() {
		if string(m.ID) == id {
			return &ProviderMeta{
				ID:             string(m.ID),
				DisplayName:    m.DisplayName,
				BackgroundSafe: m.Capabilities.BackgroundSafe,
			}
		}
	}
	return nil
}

// AddJob adds a new job to the store. Returns an error if the ID already exists.
func (s *Store) AddJob(job *Job) error {
	return s.Update(context.Background(), func(state *State) error {
		if state.Jobs == nil {
			state.Jobs = make(map[string]*Job)
		}
		if _, exists := state.Jobs[job.ID]; exists {
			return fmt.Errorf("job %q already exists", job.ID)
		}
		state.Jobs[job.ID] = job
		return nil
	})
}

// DeleteJob removes a job from the store. Returns an error if not found.
func (s *Store) DeleteJob(jobID string) error {
	return s.Update(context.Background(), func(state *State) error {
		if _, exists := state.Jobs[jobID]; !exists {
			return fmt.Errorf("job %q not found", jobID)
		}
		delete(state.Jobs, jobID)
		return nil
	})
}
