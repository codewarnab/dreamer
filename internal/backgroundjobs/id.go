package backgroundjobs

import (
	"fmt"
	"regexp"
)

const jobIDLength = 16 // 8 bytes = 16 hex chars

var jobIDPattern = regexp.MustCompile(`^[a-f0-9]{16}$`)

var reservedIDs = map[string]bool{
	"preview":   true,
	"health":    true,
	"reconcile": true,
	"options":   true,
	"templates": true,
}

// GenerateJobID returns a cryptographically random 16-character hex job ID.
func GenerateJobID() (string, error) {
	return generateRandomHex(jobIDLength)
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
