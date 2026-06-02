package stringerrtest

import (
	"errors"
	"strings"
)

// Correct: using errors.Is for sentinel matching.
var errNotFound = errors.New("not found")

func checkErrorIs(err error) bool {
	return errors.Is(err, errNotFound)
}

// Correct: using errors.As for type matching.
func checkErrorAs(err error) bool {
	var target *MyError
	return errors.As(err, &target)
}

type MyError struct{ Msg string }

func (e *MyError) Error() string { return e.Msg }

// Not flagged: .Error() on non-error type.
type notError struct{ val string }

func (n notError) Error() string { return n.val }

func nonErrorCall(n notError) bool {
	return strings.Contains(n.Error(), "something")
}

// Not flagged: strings.Contains with non-literal second arg.
func nonLiteralArg(err error, pattern string) bool {
	return strings.Contains(err.Error(), pattern)
}

// Not flagged: not strings.Contains (different function).
func notContains(err error) string {
	return err.Error()
}

// Not flagged: strings.Contains with non-Error first arg.
func nonErrorFirst(s string) bool {
	return strings.Contains(s, "hello")
}

// Not flagged: strings.Contains with method call on non-error type.
func methodOnNonError(s string) bool {
	return strings.Contains(strings.ToUpper(s), "HELLO")
}
