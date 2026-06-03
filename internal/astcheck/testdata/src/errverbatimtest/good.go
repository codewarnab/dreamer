package errverbatimtest

import (
	"fmt"
	"io"
	"net/http"
)

// Handler with safe error message — not flagged.
func handleGood1(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Handler with err.Error() logged but not sent to client.
func handleGood2(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		fmt.Println(err.Error()) // not in response body
		http.Error(w, "bad request", http.StatusBadRequest)
	}
}

// Non-handler function with err.Error() — not flagged (no ResponseWriter).
func notAHandler(err error) string {
	return fmt.Sprintf("error: %s", err.Error())
}

// Handler with static message.
func handleGood3(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		io.WriteString(w, "something went wrong") // no err.Error()
	}
}

// Handler using fmt.Fprint with static content.
func handleGood4(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		fmt.Fprint(w, "error occurred")
	}
}

// fmt.Sprintf returns a string — not a direct write. Not flagged even when
// the result contains err.Error(), because the caller may route it to
// logging or a safe message rather than the response body.
func handleGood5(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		msg := fmt.Sprintf("failed: %s", err.Error())
		fmt.Fprint(w, msg)
	}
}

// w.Write([]byte(err.Error())) is not flagged — detecting err.Error() inside
// a byte-slice conversion requires deeper AST tracking than the current
// verbatim-callee approach supports. Documented as intentionally out of scope.
func handleGood6(w http.ResponseWriter, r *http.Request) {
	err := doSomething()
	if err != nil {
		w.Write([]byte(err.Error()))
	}
}
