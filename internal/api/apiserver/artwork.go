package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// ArtworkReader answers a work's or a person's recorded cover art (section
// 21.6). Defined here and implemented by the caller, same reasoning as
// WorksReader's own doc comment: this package never imports the library store.
type ArtworkReader interface {
	GetWorkArtwork(ctx context.Context, workID int64) (WorkArtworkRow, error)
	GetPersonArtwork(ctx context.Context, personID int64) (PersonArtworkRow, error)
}

// WorkArtworkRow is the cache key, the roots for the ADR-27 visibility check, and the primary artist for the cover fallback chain.
type WorkArtworkRow struct {
	CacheKey      string
	Found         bool
	RootFolderIDs []int64
	CategoryIDs   []int64
	// PosterURL is the provider's poster for a work with no cover of its own, as a TV series has.
	PosterURL    string
	LeadPersonID *int64
}

// PersonArtworkRow is the cache key of a person's Wikidata image; people have no root or category scoping.
type PersonArtworkRow struct {
	CacheKey string
	Found    bool
}

// artworkSizeSuffix duplicates the job's on-disk size naming because core must not import internal/media/music (ADR-23).
func artworkSizeSuffix(size *string) string {
	if size == nil {
		return "small"
	}
	return *size
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

// GetWorkArtwork streams cached art from disk, falling back to poster then lead image; invisible work and no cover both answer 404 (ADR-27).
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

	// Both halves of ADR-27, as GetWork itself uses: a wanted album with no
	// files yet is visible through its category, and its cover must be too.
	rootFolderIDs, categoryIDs, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if (!sharesAnID(rootFolderIDs, row.RootFolderIDs) && !sharesAnID(categoryIDs, row.CategoryIDs)) ||
		s.keptFromWork(r.Context(), access, id) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
		return
	}

	cacheKey := row.CacheKey
	if !row.Found && row.PosterURL != "" && s.remoteImages != nil {
		if key, err := s.remoteImages.Promote(r.Context(), row.PosterURL); err == nil {
			cacheKey, row.Found = key, true
		}
	}
	if !row.Found {
		if row.LeadPersonID == nil {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
			return
		}
		person, err := s.artwork.GetPersonArtwork(r.Context(), *row.LeadPersonID)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		if !person.Found {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
			return
		}
		cacheKey = person.CacheKey
	}

	var size *string
	if params.Size != nil {
		raw := string(*params.Size)
		size = &raw
	}
	file, err := s.openLibraryImage(cacheKey, artworkSizeSuffix(size))
	if err != nil {
		// The DB row exists but the cache file does not -- a cache wipe between
		// Record and now, not a caller error. Same 404 either way: the image is
		// not available right now, and a future sweep is what fixes it.
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", imageCacheControl(r, "private, max-age=3600"))
	http.ServeContent(w, r, "", time.Time{}, file)
}

func (s *Server) routeGetPersonArtwork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a person id must be a number")
		return
	}

	var params gen.GetPersonArtworkParams
	if raw := r.URL.Query().Get("size"); raw != "" {
		if !validArtworkSizes[raw] {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "size must be one of thumb, small, full")
			return
		}
		size := gen.GetPersonArtworkParamsSize(raw)
		params.Size = &size
	}

	s.GetPersonArtwork(w, r, id, params)
}

// GetPersonArtwork streams a person's cached image; no visibility check since a person has no category scoping, and 404 hides which case applied.
func (s *Server) GetPersonArtwork(w http.ResponseWriter, r *http.Request, id int64, params gen.GetPersonArtworkParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.artwork == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "artwork is not available")
		return
	}

	row, err := s.artwork.GetPersonArtwork(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !row.Found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no image for this person")
		return
	}

	var size *string
	if params.Size != nil {
		raw := string(*params.Size)
		size = &raw
	}
	file, err := s.openLibraryImage(row.CacheKey, artworkSizeSuffix(size))
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no image for this person")
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", imageCacheControl(r, "private, max-age=3600"))
	http.ServeContent(w, r, "", time.Time{}, file)
}

// imageCacheControl keeps a picture whose URL carries its version for a year: a new version is a new URL.
func imageCacheControl(r *http.Request, unversioned string) string {
	if r.URL.Query().Get("v") != "" {
		return "private, max-age=31536000, immutable"
	}
	return unversioned
}
