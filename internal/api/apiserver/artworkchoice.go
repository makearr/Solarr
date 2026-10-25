package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// ErrImageNotOffered is a pick of an address the providers did not list.
var ErrImageNotOffered = errors.New("apiserver: that image is not one the providers offer")

// ImageOptionRow is one picture a provider offers.
type ImageOptionRow struct{ URL, ThumbURL, Source, Label string }

// ArtworkChoices browses and picks provider images for a work or an artist (M1g 1.3).
type ArtworkChoices interface {
	WorkOptions(ctx context.Context, workID int64) ([]ImageOptionRow, error)
	PersonOptions(ctx context.Context, personID int64) ([]ImageOptionRow, error)
	// PickWork and PickPerson answer ErrImageNotOffered for an address that was not listed.
	PickWork(ctx context.Context, workID int64, imageURL string) error
	PickPerson(ctx context.Context, personID int64, imageURL string) error
}

func (s *Server) routeGetWorkArtworkOptions(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a work id must be a number"); err == nil {
		s.GetWorkArtworkOptions(w, r, id)
	}
}

func (s *Server) routeChooseWorkArtwork(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a work id must be a number"); err == nil {
		s.ChooseWorkArtwork(w, r, id)
	}
}

func (s *Server) routeGetPersonArtworkOptions(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.GetPersonArtworkOptions(w, r, id)
	}
}

func (s *Server) routeChoosePersonArtwork(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a person id must be a number"); err == nil {
		s.ChoosePersonArtwork(w, r, id)
	}
}

// GetWorkArtworkOptions lists the covers providers hold for a work the caller can see.
func (s *Server) GetWorkArtworkOptions(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.workArtworkVisible(w, r, id) {
		return
	}
	options, err := s.artworkChoices.WorkOptions(r.Context(), id)
	s.writeImageOptions(w, options, err)
}

// ChooseWorkArtwork makes one of the offered covers the work's own. Administrator only: it replaces shared data.
func (s *Server) ChooseWorkArtwork(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.workArtworkVisible(w, r, id) {
		return
	}
	url, ok := s.decodeImagePick(w, r)
	if !ok {
		return
	}
	s.writeImagePicked(w, s.artworkChoices.PickWork(r.Context(), id, url))
}

// GetPersonArtworkOptions lists the pictures providers hold for an artist.
func (s *Server) GetPersonArtworkOptions(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if !s.artworkChoicesAvailable(w) {
		return
	}
	options, err := s.artworkChoices.PersonOptions(r.Context(), id)
	s.writeImageOptions(w, options, err)
}

// ChoosePersonArtwork makes one of the offered pictures the artist's own. Administrator only.
func (s *Server) ChoosePersonArtwork(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) || !s.artworkChoicesAvailable(w) {
		return
	}
	url, ok := s.decodeImagePick(w, r)
	if !ok {
		return
	}
	s.writeImagePicked(w, s.artworkChoices.PickPerson(r.Context(), id, url))
}

func (s *Server) artworkChoicesAvailable(w http.ResponseWriter) bool {
	if s.artworkChoices == nil || s.artwork == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "choosing images is not available")
		return false
	}
	return true
}

// workArtworkVisible is GetWorkArtwork's ADR-27 check: a work the caller cannot see answers 404.
func (s *Server) workArtworkVisible(w http.ResponseWriter, r *http.Request, id int64) bool {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if !s.artworkChoicesAvailable(w) {
		return false
	}
	row, err := s.artwork.GetWorkArtwork(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	rootFolderIDs, categoryIDs, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	if !sharesAnID(rootFolderIDs, row.RootFolderIDs) && !sharesAnID(categoryIDs, row.CategoryIDs) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return false
	}
	return true
}

func (s *Server) decodeImagePick(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body gen.ImagePick
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil || body.Url == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the url of an offered image")
		return "", false
	}
	return body.Url, true
}

func (s *Server) writeImageOptions(w http.ResponseWriter, options []ImageOptionRow, err error) {
	if errors.Is(err, ErrNotBuiltForMediaType) {
		writeError(w, s.logger, err)
		return
	}
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The providers could not be read", err.Error())
		return
	}
	view := gen.ImageOptions{Options: make([]gen.ImageOption, len(options))}
	for index, option := range options {
		view.Options[index] = gen.ImageOption{Url: option.URL, ThumbUrl: option.ThumbURL, Source: option.Source, Label: option.Label}
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func (s *Server) writeImagePicked(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrNotBuiltForMediaType):
		writeError(w, s.logger, err)
	case errors.Is(err, ErrImageNotOffered):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "that image is not one the providers offer")
	default:
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The image could not be fetched", err.Error())
	}
}
