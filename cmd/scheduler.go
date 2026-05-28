package cmd

import (
	"context"

	"dreamer/internal/config"
	"dreamer/internal/jobqueue"
	"dreamer/internal/logging"
)

// enqueueMissingJobs enqueues a pending job for every configured project that
// does not already have an active (pending or running) job in the queue.
func enqueueMissingJobs(ctx context.Context, queue *jobqueue.Queue, cfg *config.App, logger *logging.Logger) {
	for _, project := range cfg.Projects {
		if ctx.Err() != nil {
			return
		}
		if queue.HasActiveJob(project.Name) {
			continue
		}
		job := queue.Enqueue(project.Name, jobqueue.EnqueueConfig{
			ProjectPath: project.Path,
			Provider:    resolveProvider(cfg, project),
			Since:       project.Since,
		})
		if job != nil {
			logger.Info("enqueued analysis job", logging.Any("project", project.Name), logging.Any("job", job.ID))
		}
	}
}

// resolveProvider returns the best-fit provider id for a project by looking
// up the project-specific config file, then falling back to the global default.
func resolveProvider(cfg *config.App, project config.ProjectConfig) string {
	pfc, _ := config.LoadProjectFileConfig(project.Path)
	if pfc != nil && pfc.Provider != "" {
		return pfc.Provider
	}
	id, _ := cfg.ResolveProviderConfig(nil, "")
	return id
}
