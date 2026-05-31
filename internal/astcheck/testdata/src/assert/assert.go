// Package assert is a minimal stub for testing the theatricaltest analyzer.
// It provides the same call-site shape as github.com/stretchr/testify/assert.
package assert

import "testing"

func Len(t testing.TB, obj interface{}, length int, msgAndArgs ...interface{})       {}
func Nil(t testing.TB, obj interface{}, msgAndArgs ...interface{})                   {}
func NotNil(t testing.TB, obj interface{}, msgAndArgs ...interface{})                {}
func NotEmpty(t testing.TB, obj interface{}, msgAndArgs ...interface{})              {}
func True(t testing.TB, value bool, msgAndArgs ...interface{})                       {}
func False(t testing.TB, value bool, msgAndArgs ...interface{})                      {}
func Equal(t testing.TB, expected, actual interface{}, msgAndArgs ...interface{})    {}
func NotEqual(t testing.TB, expected, actual interface{}, msgAndArgs ...interface{}) {}
func NoError(t testing.TB, err error, msgAndArgs ...interface{})                     {}
func Error(t testing.TB, err error, msgAndArgs ...interface{})                       {}
