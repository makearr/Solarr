package apiserver

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
)

// ArtworkReader answers a work's recorded cover art (section 21.6). Defined
// here and implemented by the caller, same reasoning as WorksReader's own doc
// comment: this package never imports the library store.
type ArtworkReader interface {
	GetWorkArtwork(ctx context.Context, workID int64) (WorkArtworkRow, error)
}

// WorkArtworkRow is what ArtworkReader answers: the cache key ArtworkJob wrote
// the image under (empty when nothing has been fetched yet) and the root
// folders to run the ADR-27 visibility check against.
type WorkArtworkRow struct {
	CacheKey      string
	Found         bool
	RootFolderIDs []int64
}

// artworkSizeSuffix maps the OpenAPI enum to ArtworkJob's own on-disk naming
// (internal/media/music.artworkSizeName) -- duplicated rather than shared, since
// this package does not import internal/media/music (ADR-23: core carries no
// media-type logic).
func artworkSizeSuffix(size *gen.GetWorkArtworkParamsSize) string {
	if size == nil {
		return "small"
	}
	return string(*size)
}

// validArtworkSizes is the OpenAPI enum, checked by hand since routeGetWorkArtwork
// parses the query itself (routeListWorks' own pattern) rather than going through
// generated parameter binding.
var validArtworkSizes = map[string]bool{"thumb": true, "small": true, "full": true}

func (s *Server) routeGetWorkArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}

	var params gen.GetWorkArtworkParams
	if raw := r.URL.Query().Get("size"); raw != "" {
		if !validArtworkSizes[raw] {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "size must be one of thumb, small, full")
			return
		}
		size := gen.GetWorkArtworkParamsSize(raw)
		params.Size = &size
	}

	s.GetWorkArtwork(w, r, id, params)
}

// GetWorkArtwork streams a work's cached cover art straight off disk -- never
// fetched live, never re-encoded per request. A work the caller cannot see and
// a work with no cover recorded yet both answer 404, indistinguishable by
// design (ADR-27, Invariant 37).
func (s *Server) GetWorkArtwork(w http.ResponseWriter, r *http.Request, id int64, params gen.GetWorkArtworkParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	if s.artwork == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "artwork is not available")
		return
	}

	row, err := s.artwork.GetWorkArtwork(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	// Root-folder-only, deliberately: a subscription-discovered work has no
	// edition yet (full edition resolution for a search/subscription-identified
	// work is still unattempted, private/next.md), so ListWorksNeedingArtwork
	// never finds one to fetch a cover for -- the category half of ADR-27
	// visibility would have nothing to gate here today.
	rootFolderIDs, _, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !row.Found || !sharesAnID(rootFolderIDs, row.RootFolderIDs) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
		return
	}

	path := filepath.Join(s.imagesDir, row.CacheKey+"-"+artworkSizeSuffix(params.Size)+".webp")
	file, err := s.fsys.Open(path)
	if err != nil {
		// The DB row exists but the cache file does not -- a cache wipe between
		// Record and now, not a caller error. Same 404 either way: the image is
		// not available right now, and a future sweep is what fixes it.
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", time.Time{}, file)
}
