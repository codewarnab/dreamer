package errverbatimtest

import (
	"fmt"
	"io"
	"net/http"
)

// Handler with err.Error() in fmt.Fprintf — should be flagged.
func handleBad1(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		fmt.Fprintf(w, "error: %s", err.Error()) // want "err\\.Error\\(\\) used as HTTP response body; leaks filesystem paths — use a safe error message"
	}
}

// Handler with err.Error() in io.WriteString — should be flagged.
func handleBad2(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		io.WriteString(w, err.Error()) // want "err\\.Error\\(\\) used as HTTP response body; leaks filesystem paths — use a safe error message"
	}
}

// Method handler on a struct with ResponseWriter param.
type server struct{}

func (s *server) handleBad(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		fmt.Fprintf(w, "err: %s", err.Error()) // want "err\\.Error\\(\\) used as HTTP response body; leaks filesystem paths — use a safe error message"
	}
}

func doSomething() error { return nil }
