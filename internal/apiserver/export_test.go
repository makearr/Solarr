package apiserver

import (
	"log/slog"
	"net/http"
)

// WriteErrorForTest exposes the domain-error mapping to the package's external
// test. The mapping is the part that must not drift -- a handler that returned
// 403 where the domain said "not visible" would leak a private category -- and
// it is worth testing directly rather than only through a handler that happens
// to use it.
func WriteErrorForTest(w http.ResponseWriter, logger *slog.Logger, err error) {
	writeError(w, logger, err)
}
