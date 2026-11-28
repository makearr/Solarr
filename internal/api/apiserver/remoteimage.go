package apiserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/remoteimage"
)

// RemoteImages caches the providers' pictures; remoteimage.Cache is the real one.
type RemoteImages interface {
	// Ensure fetches the picture at the address when it is not cached and answers the cache key it is stored under.
	Ensure(ctx context.Context, address string) (string, error)
	// Promote is Ensure for a library item's picture: it ends up in the library's own directory.
	Promote(ctx context.Context, address string) (string, error)
}

// openImage opens a picture of a key, from the library's directory first and the cache second, in whatever format it
// is stored in; contentType is that format's.
func (s *Server) openImage(key, size string) (file fsx.File, contentType string, err error) {
	stem := key + "-" + size
	if s.mediaCoverDir != "" {
		if stored, found := artwork.FindFile(s.fsys, s.mediaCoverDir, stem); found {
			if file, err := s.fsys.Open(stored); err == nil {
				return file, artwork.ContentTypeOfPath(stored), nil
			}
		}
	}
	cached, _ := artwork.FindFile(s.fsys, s.imagesDir, stem)
	file, err = s.fsys.Open(cached)
	return file, artwork.ContentTypeOfPath(cached), err
}

// openLibraryImage is openImage for a library item: a picture found only in the cache is moved to the library's
// directory first, so the cache budget cannot evict it.
func (s *Server) openLibraryImage(key, size string) (fsx.File, string, error) {
	if s.mediaCoverDir != "" {
		if _, found := artwork.FindFile(s.fsys, s.mediaCoverDir, key+"-full"); !found {
			_ = remoteimage.PromoteFiles(s.fsys, s.imagesDir, s.mediaCoverDir, key)
		}
	}
	return s.openImage(key, size)
}

func (s *Server) routeGetRemoteImage(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.GetRemoteImageParams{Url: query.Get("url")}
	if raw := query.Get("size"); raw != "" {
		if !validArtworkSizes[raw] {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "size must be one of thumb, small, full")
			return
		}
		size := gen.GetRemoteImageParamsSize(raw)
		params.Size = &size
	}
	s.GetRemoteImage(w, r, params)
}

// GetRemoteImage serves a provider's picture from the local cache, fetching it once. Any signed-in user may ask, but
// only the providers' own image hosts are ever fetched.
func (s *Server) GetRemoteImage(w http.ResponseWriter, r *http.Request, params gen.GetRemoteImageParams) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.remoteImages == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "images are not available")
		return
	}
	key, err := s.remoteImages.Ensure(r.Context(), params.Url)
	switch {
	case errors.Is(err, remoteimage.ErrNotAllowed):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "that is not an image address Solarr serves")
		return
	case err != nil:
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the picture is not available")
		return
	}
	var size *string
	if params.Size != nil {
		raw := string(*params.Size)
		size = &raw
	}
	file, contentType, err := s.openImage(key, artworkSizeSuffix(size))
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the picture is not available")
		return
	}
	defer func() { _ = file.Close() }()
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", imageCacheControl(r, "private, max-age=86400"))
	http.ServeContent(w, r, "", time.Time{}, file)
}
