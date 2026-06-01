//go:build !windows

package backgroundjobs

import (
	"context"

	"dreamer/internal/logging"
)

// ActivityMonitor is a no-op on non-Windows platforms. Linux/macOS have
// stronger OS-level sandboxing (bwrap, seatbelt) that provides process
// and network isolation at the kernel level.
type ActivityMonitor struct{}

// NewActivityMonitor returns a no-op monitor on non-Windows platforms.
func NewActivityMonitor(_ uintptr, _ uint32, _ *ActivityStore, _ *logging.Logger) *ActivityMonitor {
	return &ActivityMonitor{}
}

// Start is a no-op on non-Windows.
func (m *ActivityMonitor) Start(_ context.Context) error { return nil }

// Stop returns a zero summary on non-Windows.
func (m *ActivityMonitor) Stop() ActivitySummary { return ActivitySummary{} }
