package apiserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/makearr/MediaNexus/internal/api/authz"
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

// CategoriesWithOfflineFilesForTest exposes todo 4.18's own settings.db/
// library.db composition to the package's external test.
func (s *Server) CategoriesWithOfflineFilesForTest(ctx context.Context, access authz.Access) []CategoryOfflineFiles {
	return s.categoriesWithOfflineFiles(ctx, access)
}
