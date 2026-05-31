package unboundedreadtest

import (
	"io"
	"net/http"
	"os"
)

func badStdin() {
	_, _ = io.ReadAll(os.Stdin) // want "io.ReadAll on os.Stdin without a size cap"
}

func badBody(r *http.Request) {
	_, _ = io.ReadAll(r.Body) // want "io.ReadAll on http.Request.Body without a size cap"
}

func badHandler(w http.ResponseWriter, req *http.Request) {
	_, _ = io.ReadAll(req.Body) // want "io.ReadAll on http.Request.Body without a size cap"
	_ = w
}
