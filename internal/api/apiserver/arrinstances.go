package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/config"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

// ListArrInstances answers every configured *Arr connection. api_key is
// always masked -- settings.ArrInstance never carries the raw value at all.
func (s *Server) ListArrInstances(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	instances, err := s.auth.Settings.ArrInstances(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ArrInstance, len(instances))
	for index, instance := range instances {
		views[index] = arrInstanceView(instance)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddArrInstance connects a new *Arr instance. Administrator only -- it
// holds a credential from the moment it is created.
func (s *Server) AddArrInstance(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.AddArrInstanceJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with app, name, base_url and api_key")
		return
	}

	created, err := s.auth.Settings.CreateArrInstance(r.Context(), settings.ArrInstanceRequest{
		App: settings.ArrApp(body.App), Name: body.Name, BaseURL: body.BaseUrl,
		CategoryID: body.CategoryId, Enabled: derefOr(body.Enabled, true),
	}, secrets.Secret(body.ApiKey), s.wall.Now().Unix())
	if err != nil {
		writeArrInstanceError(w, s, err, "The connection has problems", "Nothing was created.")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, arrInstanceView(created))
}

// routeUpdateArrInstance parses the id path parameter, mirroring routeUpdateExclusionPattern.
func (s *Server) routeUpdateArrInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.UpdateArrInstance(w, r, id)
}

// UpdateArrInstance changes a connection's own facts. Administrator only.
// Never touches api_key -- SetArrInstanceKey's own job.
func (s *Server) UpdateArrInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateArrInstanceJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name and base_url")
		return
	}

	existing, err := s.auth.Settings.GetArrInstance(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such instance")
		return
	}
	updated, err := s.auth.Settings.UpdateArrInstance(r.Context(), id, settings.ArrInstanceRequest{
		App: existing.App, Name: body.Name, BaseURL: body.BaseUrl,
		CategoryID: body.CategoryId, Enabled: derefOr(body.Enabled, true),
	}, s.wall.Now().Unix())
	if err != nil {
		writeArrInstanceError(w, s, err, "The connection has problems", "Nothing was changed.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, arrInstanceView(updated))
}

// routeDeleteArrInstance parses the id path parameter, mirroring routeDeleteDownloadFolder.
func (s *Server) routeDeleteArrInstance(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.DeleteArrInstance(w, r, id)
}

// DeleteArrInstance removes a connection. Administrator only.
func (s *Server) DeleteArrInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteArrInstance(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeSetArrInstanceKey parses the id path parameter.
func (s *Server) routeSetArrInstanceKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an instance id must be a number")
		return
	}
	s.SetArrInstanceKey(w, r, id)
}

// SetArrInstanceKey replaces a connection's own credential. Administrator only.
func (s *Server) SetArrInstanceKey(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.SetArrInstanceKeyJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with api_key")
		return
	}
	if err := s.auth.Settings.SetArrInstanceAPIKey(r.Context(), id, secrets.Secret(body.ApiKey), s.wall.Now().Unix()); err != nil {
		writeArrInstanceError(w, s, err, "The api key has problems", "Nothing was changed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeArrInstanceError translates settings.ArrInstance's own error
// vocabulary the same way AddDownloadFolder already does for its own
// config.Problems/settings.ErrNotFound pair, plus settings.ErrSecretsUnavailable
// (the process started with no encryption key decoded -- a startup
// condition, not a caller mistake, so it answers 503 like every other
// "not available" capability rather than 400).
func writeArrInstanceError(w http.ResponseWriter, s *Server, err error, title, closing string) {
	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, title, problems.Report("the connection", closing))
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such category")
	case errors.Is(err, settings.ErrSecretsUnavailable):
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "no encryption key is configured; an api key cannot be stored")
	default:
		writeError(w, s.logger, err)
	}
}

func arrInstanceView(instance settings.ArrInstance) gen.ArrInstance {
	// HasValue alone, no Masked hint: showing even a masked hint (secrets.
	// Secret.Mask()) would mean decrypting the key for a plain list view --
	// this package never does that outside the one call site that actually
	// needs the raw value (the sync job's own REST client, via
	// Cache.DecryptedAPIKey), so a list response has nothing to hint from.
	view := gen.ArrInstance{
		Id: instance.ID, App: gen.ArrApp(instance.App), Name: instance.Name, BaseUrl: instance.BaseURL,
		CategoryId: instance.CategoryID, Enabled: instance.Enabled,
		ApiKey:    gen.SecretMask{HasValue: true},
		CreatedAt: unixTime(instance.CreatedAt), UpdatedAt: unixTime(instance.UpdatedAt),
	}
	if instance.LastSyncedAt != nil {
		at := unixTime(*instance.LastSyncedAt)
		view.LastSyncedAt = &at
	}
	if instance.LastSyncError != "" {
		reason := instance.LastSyncError
		view.LastSyncError = &reason
	}
	return view
}

// unixTime converts a stored unix-seconds column into the time.Time every
// API timestamp is rendered as (date-time, matching every other schema's
// own created_at/started_at).
func unixTime(seconds int64) time.Time { return time.Unix(seconds, 0).UTC() }
