package state

import "time"

// FindingStateStatus enumerates the lifecycle states a finding can hold
// once the user has touched it. Open findings are absent from the map.
const (
	FindingStatusApplied   = "applied"
	FindingStatusDismissed = "dismissed"
	FindingStatusResolved  = "resolved"
)

// FindingState tracks the lifecycle of a single finding keyed by its
// canonical hash (the same value rendered as
// <!-- dreamer:finding:<hex> --> in todos.md).
type FindingState struct {
	Status          string           `json:"status"`
	AppliedAt       time.Time        `json:"applied_at,omitempty"`
	AppliedReversal *FindingReversal `json:"applied_reversal,omitempty"`
	DismissedAt     time.Time        `json:"dismissed_at,omitempty"`
	ResolvedAt      time.Time        `json:"resolved_at,omitempty"`
	ProjectName     string           `json:"project_name,omitempty"`
}

// FindingReversal captures everything required to undo an applied
// finding. PreImage is stored verbatim; the 4 MiB cap is enforced at
// apply time.
type FindingReversal struct {
	Path            string `json:"path"`
	Strategy        string `json:"strategy"`
	PreImageSHA256  string `json:"pre_sha"`
	PostImageSHA256 string `json:"post_sha"`
	PreImage        string `json:"pre_image,omitempty"`
}
