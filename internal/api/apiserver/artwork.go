package apiserver

import (
	"context"
	"net/http"
	"path/filepath"
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

// WorkArtworkRow is what ArtworkReader answers: the cache key ArtworkJob wrote
// the image under (empty when nothing has been fetched yet), the root
// folders to run the ADR-27 visibility check against, and its primary
// artist -- the album-falls-back-to-artist-image chain (2026-09-11).
type WorkArtworkRow struct {
	CacheKey      string
	Found         bool
	RootFolderIDs []int64
	CategoryIDs   []int64
	LeadPersonID  *int64
}

// PersonArtworkRow is what ArtworkReader answers for a person: the cache key
// EnrichJob wrote their Wikidata image under, if any. No visibility fields
// -- a person has no root-folder or category scoping of its own (this
// file's own GetPersonArtwork doc comment).
type PersonArtworkRow struct {
	CacheKey string
	Found    bool
}

// artworkSizeSuffix maps the OpenAPI enum to ArtworkJob's own on-disk naming
// (internal/media/music.artworkSizeName) -- duplicated rather than shared, since
// this package does not import internal/media/music (ADR-23: core carries no
// media-type logic). A plain *string rather than one generated enum type, so
// both GetWorkArtwork's and GetPersonArtwork's own (distinct, generated)
// params types can share it.
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

// GetWorkArtwork streams a work's cached cover art straight off disk -- never
// fetched live, never re-encoded per request. A work the caller cannot see
// answers 404. A work the caller can see but with no cover of its own falls
// back to its primary artist's image (2026-09-11's own album-falls-back-to-
// artist chain) before answering 404 too -- indistinguishable by design from
// "no cover recorded yet" either way (ADR-27, Invariant 37).
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
	if !sharesAnID(rootFolderIDs, row.RootFolderIDs) && !sharesAnID(categoryIDs, row.CategoryIDs) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no cover art for this work")
		return
	}

	cacheKey := row.CacheKey
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
	path := filepath.Join(s.imagesDir, cacheKey+"-"+artworkSizeSuffix(size)+".webp")
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

// GetPersonArtwork streams a person's cached Wikidata image straight off
// disk. No ADR-27 visibility check -- a person is a shared entity with no
// category scoping of its own (GetPerson itself has none either); "no such
// person" and "no image recorded" both answer 404, the same
// indistinguishable-by-design GetWorkArtwork already keeps.
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
	path := filepath.Join(s.imagesDir, row.CacheKey+"-"+artworkSizeSuffix(size)+".webp")
	file, err := s.fsys.Open(path)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no image for this person")
		return
	}
	defer func() { _ = file.Close() }()

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", time.Time{}, file)
}
