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
	Status          string            `json:"status"`
	AppliedAt       time.Time         `json:"applied_at,omitempty"`
	AppliedReversal *FindingReversal  `json:"applied_reversal,omitempty"`
	DismissedAt     time.Time         `json:"dismissed_at,omitempty"`
	ResolvedAt      time.Time         `json:"resolved_at,omitempty"`
	ProjectName     string            `json:"project_name,omitempty"`
	ApplySpec       *FindingApplySpec `json:"apply_spec,omitempty"`
}

// FindingApplySpec is the server-trusted apply plan emitted by the
// analyzer when the finding was first recorded. The Apply handler reads
// these fields by hash; the request body's apply fields are ignored.
type FindingApplySpec struct {
	Category   string `json:"category"`
	TargetFile string `json:"target_file"`
	Strategy   string `json:"strategy"`
	Anchor     string `json:"anchor,omitempty"`
	Snippet    string `json:"snippet"`
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
