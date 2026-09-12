package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

// ListIndexers answers every configured indexer connection. api_key is
// always masked -- settings.Indexer never carries the raw value at all.
func (s *Server) ListIndexers(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	indexers, err := s.auth.Settings.Indexers(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.Indexer, len(indexers))
	for index, indexer := range indexers {
		views[index] = indexerView(indexer)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddIndexer connects a new indexer. Administrator only -- it may hold a
// credential from the moment it is created.
func (s *Server) AddIndexer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.AddIndexerJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name, impl and base_url")
		return
	}

	created, err := s.auth.Settings.CreateIndexer(r.Context(), settings.IndexerRequest{
		Name: body.Name, Impl: body.Impl, BaseURL: body.BaseUrl,
		Enabled: derefOr(body.Enabled, true), Priority: derefOr(body.Priority, 50),
	}, secrets.Secret(derefOr(body.ApiKey, "")), s.wall.Now().Unix())
	if err != nil {
		writeIndexerError(w, s, err, "The connection has problems", "Nothing was created.")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, indexerView(created))
}

// routeUpdateIndexer parses the id path parameter, mirroring routeUpdateArrInstance.
func (s *Server) routeUpdateIndexer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an indexer id must be a number")
		return
	}
	s.UpdateIndexer(w, r, id)
}

// UpdateIndexer changes a connection's own facts. Administrator only. Never
// touches api_key -- SetIndexerAPIKey's own job.
func (s *Server) UpdateIndexer(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateIndexerJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name, impl and base_url")
		return
	}

	updated, err := s.auth.Settings.UpdateIndexer(r.Context(), id, settings.IndexerRequest{
		Name: body.Name, Impl: body.Impl, BaseURL: body.BaseUrl,
		Enabled: derefOr(body.Enabled, true), Priority: derefOr(body.Priority, 50),
	}, s.wall.Now().Unix())
	if err != nil {
		writeIndexerError(w, s, err, "The connection has problems", "Nothing was changed.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, indexerView(updated))
}

// routeDeleteIndexer parses the id path parameter, mirroring routeDeleteArrInstance.
func (s *Server) routeDeleteIndexer(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an indexer id must be a number")
		return
	}
	s.DeleteIndexer(w, r, id)
}

// DeleteIndexer removes a connection. Administrator only.
func (s *Server) DeleteIndexer(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.auth.Settings.GetIndexer(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such indexer")
		return
	}
	if err := s.auth.Settings.DeleteIndexer(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeSetIndexerAPIKey parses the id path parameter.
func (s *Server) routeSetIndexerAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an indexer id must be a number")
		return
	}
	s.SetIndexerAPIKey(w, r, id)
}

// SetIndexerAPIKey replaces or clears a connection's own credential.
// Administrator only. An empty/absent api_key clears it -- a public
// Torznab endpoint may need none at all (ADR-47).
func (s *Server) SetIndexerAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.SetIndexerAPIKeyJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object")
		return
	}
	if err := s.auth.Settings.SetIndexerAPIKey(r.Context(), id, secrets.Secret(derefOr(body.ApiKey, "")), s.wall.Now().Unix()); err != nil {
		writeIndexerError(w, s, err, "The api key has problems", "Nothing was changed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeIndexerError translates settings.Indexer's own error vocabulary the
// same way writeArrInstanceError already does for ArrInstance's identical
// shape.
func writeIndexerError(w http.ResponseWriter, s *Server, err error, title, closing string) {
	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, title, problems.Report("the connection", closing))
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such indexer")
	case errors.Is(err, settings.ErrSecretsUnavailable):
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "no encryption key is configured; an api key cannot be stored")
	default:
		writeError(w, s.logger, err)
	}
}

func indexerView(indexer settings.Indexer) gen.Indexer {
	view := gen.Indexer{
		Id: indexer.ID, Name: indexer.Name, Impl: indexer.Impl, BaseUrl: indexer.BaseURL,
		Enabled: indexer.Enabled, Priority: indexer.Priority,
		ApiKey:    gen.SecretMask{HasValue: indexer.HasAPIKey},
		CreatedAt: unixTime(indexer.CreatedAt), UpdatedAt: unixTime(indexer.UpdatedAt),
	}
	if indexer.CategoriesJSON != "" {
		categories := indexer.CategoriesJSON
		view.CategoriesJson = &categories
	}
	if indexer.CapsFetchedAt != nil {
		at := unixTime(*indexer.CapsFetchedAt)
		view.CapsFetchedAt = &at
	}
	return view
}
