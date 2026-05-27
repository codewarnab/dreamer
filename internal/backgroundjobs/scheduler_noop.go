//go:build !windows && !linux && !darwin

package backgroundjobs

import (
	"context"
	"fmt"
	"runtime"

	"dreamer/internal/logging"
)

type noopScheduler struct{}

func newPlatformScheduler(_ SchedulerConfig, _ *logging.Logger) Scheduler {
	return &noopScheduler{}
}

func (s *noopScheduler) Install(_ context.Context, _ ScheduleParams) (OSScheduleState, error) {
	return OSScheduleState{}, fmt.Errorf("background scheduling not available on %q", runtime.GOOS)
}

func (s *noopScheduler) Update(_ context.Context, _ ScheduleParams) (OSScheduleState, error) {
	return OSScheduleState{}, fmt.Errorf("background scheduling not available on %q", runtime.GOOS)
}

func (s *noopScheduler) Remove(_ context.Context, _ string) error {
	return fmt.Errorf("background scheduling not available on %q", runtime.GOOS)
}

func (s *noopScheduler) Inspect(_ context.Context, _ string) (ScheduleHealth, error) {
	return ScheduleHealth{}, fmt.Errorf("background scheduling not available on %q", runtime.GOOS)
}

func (s *noopScheduler) ListOwn(_ context.Context) ([]string, error) {
	return nil, nil // no schedules to list
}
