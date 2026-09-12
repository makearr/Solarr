package apiserver

import (
	"log/slog"
	"net/http"
)

// WriteErrorForTest exposes the domain-error mapping to the package's external test.
func WriteErrorForTest(w http.ResponseWriter, logger *slog.Logger, err error) {
	writeError(w, logger, err)
}

// IsNearReserveForTest exposes todo 4.8's own "near reserve" threshold to
// the package's external test.
func IsNearReserveForTest(online bool, freeBytes *int64, minFreeBytes int64) bool {
	return isNearReserve(online, freeBytes, minFreeBytes)
}
