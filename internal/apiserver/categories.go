package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/settings"
)

// ListCategories returns every category the caller may read (ADR-27), for
// navigation rather than access management -- GetMe's categories carry a
// permission field for that purpose, this is deliberately narrower.
func (s *Server) ListCategories(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	visible := access.Categories(settings.PermissionRead)
	views := make([]gen.LibraryCategory, len(visible))
	for index, category := range visible {
		works, files := s.categoryCounts(r.Context(), category.ID)
		views[index] = gen.LibraryCategory{
			Id: category.ID, MediaType: category.MediaType, Name: category.Name,
			Works: &works, Files: &files,
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// categoryCounts reads the "works"/"files" metrics library.CategoryCounters
// reconciles (never a live COUNT, section 21.12). Unavailable or never-reconciled
// answers 0, the same "render without the tile" fallback libraryCounts uses,
// rather than failing the whole category list over one figure.
func (s *Server) categoryCounts(ctx context.Context, categoryID int64) (works, files int) {
	if s.librarySize == nil {
		return 0, 0
	}
	values, err := s.librarySize.Counters(ctx, fmt.Sprintf("category:%d", categoryID))
	if err != nil {
		s.logger.WarnContext(ctx, "category counters unavailable",
			"category_id", categoryID, "error", err)
		return 0, 0
	}
	return int(values["works"]), int(values["files"])
}
