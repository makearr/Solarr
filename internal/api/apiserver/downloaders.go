package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// ListDownloaders answers every configured download client connection.
// credentials is always masked -- settings.Downloader never carries the raw
// value at all.
func (s *Server) ListDownloaders(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	downloaders, err := s.auth.Settings.Downloaders(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.Downloader, len(downloaders))
	for index, downloader := range downloaders {
		views[index] = downloaderView(downloader)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddDownloader connects a new download client. Administrator only -- it
// may hold a credential from the moment it is created.
func (s *Server) AddDownloader(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.AddDownloaderJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name, impl, base_url and protocol")
		return
	}

	created, err := s.auth.Settings.CreateDownloader(r.Context(), settings.DownloaderRequest{
		Name: body.Name, Impl: body.Impl, BaseURL: body.BaseUrl,
		Enabled: derefOr(body.Enabled, true), Protocol: string(body.Protocol),
	}, secrets.Secret(derefOr(body.Credentials, "")), s.wall.Now().Unix())
	if err != nil {
		writeDownloaderError(w, s, err, "The connection has problems", "Nothing was created.")
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, downloaderView(created))
}

// routeUpdateDownloader parses the id path parameter, mirroring routeUpdateIndexer.
func (s *Server) routeUpdateDownloader(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a downloader id must be a number")
		return
	}
	s.UpdateDownloader(w, r, id)
}

// UpdateDownloader changes a connection's own facts. Administrator only.
// Never touches credentials -- SetDownloaderCredentials's own job.
func (s *Server) UpdateDownloader(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.UpdateDownloaderJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name, impl, base_url and protocol")
		return
	}

	updated, err := s.auth.Settings.UpdateDownloader(r.Context(), id, settings.DownloaderRequest{
		Name: body.Name, Impl: body.Impl, BaseURL: body.BaseUrl,
		Enabled: derefOr(body.Enabled, true), Protocol: string(body.Protocol),
	}, s.wall.Now().Unix())
	if err != nil {
		writeDownloaderError(w, s, err, "The connection has problems", "Nothing was changed.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, downloaderView(updated))
}

// routeDeleteDownloader parses the id path parameter, mirroring routeDeleteIndexer.
func (s *Server) routeDeleteDownloader(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a downloader id must be a number")
		return
	}
	s.DeleteDownloader(w, r, id)
}

// DeleteDownloader removes a connection. Administrator only.
func (s *Server) DeleteDownloader(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.auth.Settings.GetDownloader(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such downloader")
		return
	}
	if err := s.auth.Settings.DeleteDownloader(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeSetDownloaderCredentials parses the id path parameter.
func (s *Server) routeSetDownloaderCredentials(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a downloader id must be a number")
		return
	}
	s.SetDownloaderCredentials(w, r, id)
}

// SetDownloaderCredentials replaces or clears a connection's own
// credentials. Administrator only.
func (s *Server) SetDownloaderCredentials(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var body gen.SetDownloaderCredentialsJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object")
		return
	}
	if err := s.auth.Settings.SetDownloaderCredentials(r.Context(), id, secrets.Secret(derefOr(body.Credentials, "")), s.wall.Now().Unix()); err != nil {
		writeDownloaderError(w, s, err, "The credentials have problems", "Nothing was changed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeTestDownloader parses the id path parameter, mirroring routeTestIndexer.
func (s *Server) routeTestDownloader(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a downloader id must be a number")
		return
	}
	s.TestDownloader(w, r, id)
}

// DownloaderTester runs todo 5.2's own "every connection tested before it is
// accepted" against the real download client -- SABnzbd's own Version()
// call, never a queue or dispatch action.
type DownloaderTester interface {
	TestDownloader(ctx context.Context, id int64) (ConnectionTestResult, error)
}

// TestDownloader runs a connection test against the real download client.
func (s *Server) TestDownloader(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	result, err := s.downloaderTester.TestDownloader(r.Context(), id)
	if err != nil {
		writeDownloaderError(w, s, err, "The connection has problems", "Nothing was tested.")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ConnectionTestResult{
		Ok: result.OK, Message: result.Message, TestedAt: result.TestedAt,
	})
}

// writeDownloaderError translates settings.Downloader's own error
// vocabulary the same way writeIndexerError already does for Indexer's
// identical shape.
func writeDownloaderError(w http.ResponseWriter, s *Server, err error, title, closing string) {
	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, title, problems.Report("the connection", closing))
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such downloader")
	case errors.Is(err, settings.ErrSecretsUnavailable):
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "no encryption key is configured; credentials cannot be stored")
	default:
		writeError(w, s.logger, err)
	}
}

func downloaderView(downloader settings.Downloader) gen.Downloader {
	return gen.Downloader{
		Id: downloader.ID, Name: downloader.Name, Impl: downloader.Impl, BaseUrl: downloader.BaseURL,
		Enabled: downloader.Enabled, Protocol: gen.DownloaderProtocol(downloader.Protocol),
		Credentials: gen.SecretMask{HasValue: downloader.HasCredentials},
		CreatedAt:   unixTime(downloader.CreatedAt), UpdatedAt: unixTime(downloader.UpdatedAt),
	}
}
