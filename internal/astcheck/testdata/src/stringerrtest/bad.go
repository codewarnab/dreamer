package stringerrtest

import (
	"errors"
	"strings"
)

func checkErrorBad(err error) bool {
	return strings.Contains(err.Error(), "not found") // want "error type detected via strings.Contains\\(err\\.Error\\(\\), \\.\\.\\.\\); use errors\\.Is or errors\\.As instead"
}

func checkErrorBadWrapped(err error) bool {
	return strings.Contains(err.Error(), "permission denied") // want "error type detected via strings.Contains\\(err\\.Error\\(\\), \\.\\.\\.\\); use errors\\.Is or errors\\.As instead"
}

func checkErrorBadCustom(err error) string {
	if strings.Contains(err.Error(), "timeout") { // want "error type detected via strings.Contains\\(err\\.Error\\(\\), \\.\\.\\.\\); use errors\\.Is or errors\\.As instead"
		return "timed out"
	}
	return ""
}

// Sentinel error matching — should be flagged.
var errLimit = errors.New("limit reached")

func checkSentinel(err error) bool {
	return strings.Contains(err.Error(), "limit reached") // want "error type detected via strings.Contains\\(err\\.Error\\(\\), \\.\\.\\.\\); use errors\\.Is or errors\\.As instead"
}
