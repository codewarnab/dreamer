package backgroundjobs

import (
	"fmt"
	"regexp"
)

const jobIDHexLen = 16 // 8 bytes = 16 hex chars

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

// reservedIDs lists route segments that RouteJobs dispatches to dedicated handlers.
// Keep in sync with the dispatch table in internal/web/handlers/jobs.go:RouteJobs.
var reservedIDs = map[string]bool{
	"preview":   true,
	"health":    true,
	"reconcile": true,
	"options":   true,
	"templates": true,
	"audit":     true,
}

// GenerateJobID returns a cryptographically random 16-character hex job ID.
func GenerateJobID() (string, error) {
	return generateRandomHex(jobIDHexLen)
}

// ValidateJobID checks that id is a valid 16-lowercase-hex job ID that does
// not collide with reserved API route segments.
func ValidateJobID(id string) error {
	if reservedIDs[id] {
		return fmt.Errorf("job ID %q is reserved", id)
	}
	if !jobIDPattern.MatchString(id) {
		return fmt.Errorf("job ID %q invalid: must be 16 lowercase hex chars", id)
	}
	return nil
}
