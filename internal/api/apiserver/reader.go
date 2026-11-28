package apiserver

import (
	"context"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/authz"
)

// ComicPages lists and reads the pages of a comic file for the reader; implemented in cmd/solarr/adapters.
type ComicPages interface {
	// PageNames lists the page images in reading order.
	PageNames(ctx context.Context, issue string) ([]string, error)
	// ReadPage is one page's bytes and media type.
	ReadPage(ctx context.Context, issue, name string) (data []byte, contentType string, err error)
}

// ReadingPlace is where a user stopped reading a file.
type ReadingPlace struct {
	Position  string
	Progress  float64
	UpdatedAt int64
}

// ReadingPlaces keeps each user's place in each file.
type ReadingPlaces interface {
	Get(ctx context.Context, userID, mediaObjectID int64) (ReadingPlace, bool, error)
	Set(ctx context.Context, userID, mediaObjectID int64, place ReadingPlace) error
}

// comicExtensions are the files the page reader opens; an EPUB and a PDF are opened by the browser from the file itself.
var comicExtensions = []string{".cbz", ".cbr", ".cb7", ".cbt"}

// readableDocument says the file is one the browser's reader opens from the file's own bytes.
func readableDocument(absolute string) bool {
	extension := strings.ToLower(path.Ext(absolute))
	return extension == ".epub" || extension == ".pdf"
}

func isComicFile(absolute string) bool {
	extension := strings.ToLower(path.Ext(absolute))
	for _, one := range comicExtensions {
		if extension == one {
			return true
		}
	}
	return false
}

// routeGetFilePages answers how many pages a comic file has.
func (s *Server) routeGetFilePages(w http.ResponseWriter, r *http.Request) {
	_, names, ok := s.comicFile(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, s.logger, http.StatusOK, map[string]int{"count": len(names)})
}

// routeGetFilePage streams one page image of a comic file.
func (s *Server) routeGetFilePage(w http.ResponseWriter, r *http.Request) {
	file, names, ok := s.comicFile(w, r)
	if !ok {
		return
	}
	index, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil || index < 0 || index >= len(names) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such page")
		return
	}
	data, contentType, err := s.comicPages.ReadPage(r.Context(), file.AbsolutePath, names[index])
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the page could not be read")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(data)
}

// comicFile finds the file for the caller and lists its pages, or writes the problem.
func (s *Server) comicFile(w http.ResponseWriter, r *http.Request) (PlaybackFile, []string, bool) {
	if s.comicPages == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the comic reader is not available")
		return PlaybackFile{}, nil, false
	}
	file, ok := s.visibleFile(w, r)
	if !ok {
		return PlaybackFile{}, nil, false
	}
	if !isComicFile(file.AbsolutePath) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "this file is not a comic archive")
		return PlaybackFile{}, nil, false
	}
	names, err := s.comicPages.PageNames(r.Context(), file.AbsolutePath)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "the comic could not be opened")
		return PlaybackFile{}, nil, false
	}
	return file, names, true
}

// readingPosition is the body of a reading position.
type readingPosition struct {
	Position string  `json:"position"`
	Progress float64 `json:"progress"`
}

// routeGetReadingPosition answers the caller's place in a file; 404 when they have none.
func (s *Server) routeGetReadingPosition(w http.ResponseWriter, r *http.Request) {
	userID, file, ok := s.readingContext(w, r)
	if !ok {
		return
	}
	place, found, err := s.readingPlaces.Get(r.Context(), userID, file.MediaObjectID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no reading position yet")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, readingPosition{Position: place.Position, Progress: place.Progress})
}

// routeSetReadingPosition keeps the caller's place in a file.
func (s *Server) routeSetReadingPosition(w http.ResponseWriter, r *http.Request) {
	userID, file, ok := s.readingContext(w, r)
	if !ok {
		return
	}
	var body readingPosition
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a position and a progress") {
		return
	}
	if body.Position == "" || len(body.Position) > 2048 || body.Progress < 0 || body.Progress > 1 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the position must be a short text and the progress between 0 and 1")
		return
	}
	err := s.readingPlaces.Set(r.Context(), userID, file.MediaObjectID, ReadingPlace{Position: body.Position, Progress: body.Progress, UpdatedAt: s.auth.Clock.Now().Unix()})
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readingContext(w http.ResponseWriter, r *http.Request) (int64, PlaybackFile, bool) {
	if s.readingPlaces == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "reading positions are not available")
		return 0, PlaybackFile{}, false
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return 0, PlaybackFile{}, false
	}
	file, ok := s.visibleFile(w, r)
	if !ok {
		return 0, PlaybackFile{}, false
	}
	return access.UserID(), file, true
}

// The generated entry points; the id is read from the path by the routes.
func (s *Server) GetFilePages(w http.ResponseWriter, r *http.Request, _ int64) {
	s.routeGetFilePages(w, r)
}
func (s *Server) GetFilePage(w http.ResponseWriter, r *http.Request, _ int64, _ int) {
	s.routeGetFilePage(w, r)
}
func (s *Server) GetReadingPosition(w http.ResponseWriter, r *http.Request, _ int64) {
	s.routeGetReadingPosition(w, r)
}
func (s *Server) SetReadingPosition(w http.ResponseWriter, r *http.Request, _ int64) {
	s.routeSetReadingPosition(w, r)
}
