package apiserver

import (
	"log/slog"
	"net/http"
)

// WriteErrorForTest exposes the domain-error mapping to the package's external test.
func WriteErrorForTest(w http.ResponseWriter, logger *slog.Logger, err error) {
	writeError(w, logger, err)
}
