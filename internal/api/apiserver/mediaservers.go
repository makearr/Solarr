package apiserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

// MediaServerClients builds the client of a connection, saved or not; an unknown kind is an error.
type MediaServerClients func(kind, baseURL, username string, credential secrets.Secret) (mediaserver.Server, error)

// ListMediaServerKinds answers what each kind of server can do.
func (s *Server) ListMediaServerKinds(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	kinds := mediaserver.Kinds()
	views := make([]gen.MediaServerKind, len(kinds))
	for index, kind := range kinds {
		view := gen.MediaServerKind{
			Key: kind.Key, Label: kind.Label, Credential: gen.MediaServerKindCredential(kind.Credential), CanPush: kind.CanPush,
			PushByPath: kind.PushByPath, CanAnnounce: kind.CanAnnounce, CanPlayback: kind.CanPlayback, CanMetadata: kind.CanMetadata,
			HasUsers: kind.HasUsers, NeedsAdmin: kind.NeedsAdmin, MediaTypes: kind.MediaTypes,
		}
		if kind.DefaultPortHint != "" {
			view.DefaultPortHint = &kind.DefaultPortHint
		}
		views[index] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// ListMediaServers answers every connection with its mappings.
func (s *Server) ListMediaServers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	servers, err := s.auth.Settings.MediaServers(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.MediaServer, len(servers))
	for index, server := range servers {
		if views[index], err = s.mediaServerView(r.Context(), server); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// GetMediaServer answers one connection.
func (s *Server) GetMediaServer(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.writeMediaServer(w, r, id, http.StatusOK)
}

// AddMediaServer connects a media server.
func (s *Server) AddMediaServer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.AddMediaServerJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with kind, name, base_url and credential") {
		return
	}
	if _, found := mediaserver.KindOf(body.Kind); !found {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Unknown kind", fmt.Sprintf("%q is not a media server kind Solarr knows", body.Kind))
		return
	}
	created, err := s.auth.Settings.CreateMediaServer(r.Context(), mediaServerRequest(body), secrets.Secret(derefOr(body.Credential, "")), s.wall.Now().Unix())
	if err != nil {
		writeArrInstanceError(w, s, err, "The connection has problems", "Nothing was created.")
		return
	}
	s.writeMediaServer(w, r, created.ID, http.StatusCreated)
}

// UpdateMediaServer replaces a connection's facts; the credential changes only when one is sent.
func (s *Server) UpdateMediaServer(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.UpdateMediaServerJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with name and base_url") {
		return
	}
	existing, err := s.auth.Settings.GetMediaServer(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	body.Kind = existing.Kind
	now := s.wall.Now().Unix()
	if _, err := s.auth.Settings.UpdateMediaServer(r.Context(), id, mediaServerRequest(body), now); err != nil {
		writeArrInstanceError(w, s, err, "The connection has problems", "Nothing was changed.")
		return
	}
	if credential := derefOr(body.Credential, ""); credential != "" {
		if err := s.auth.Settings.SetMediaServerCredential(r.Context(), id, secrets.Secret(credential), now); err != nil {
			writeArrInstanceError(w, s, err, "The credential has problems", "The credential was not changed.")
			return
		}
	}
	s.writeMediaServer(w, r, id, http.StatusOK)
}

// DeleteMediaServer removes a connection.
func (s *Server) DeleteMediaServer(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteMediaServer(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestMediaServer tries a connection and reports what the server holds.
func (s *Server) TestMediaServer(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.mediaServerClients == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "media server connections are not wired in this build")
		return
	}
	var body gen.TestMediaServerJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with id or connection") {
		return
	}
	var kind, baseURL, username string
	var credential secrets.Secret
	var stored *settings.MediaServer
	if body.Id != nil {
		existing, err := s.auth.Settings.GetMediaServer(r.Context(), *body.Id)
		if err != nil {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
			return
		}
		stored = &existing
		kind, baseURL, username = existing.Kind, existing.BaseURL, existing.Username
	}
	if connection := body.Connection; connection != nil {
		kind, baseURL, username = connection.Kind, connection.BaseUrl, derefOr(connection.Username, "")
		credential = secrets.Secret(derefOr(connection.Credential, ""))
	}
	if stored != nil && credential.IsZero() {
		opened, err := s.auth.Settings.DecryptedMediaServerCredential(r.Context(), stored.ID)
		if err != nil {
			writeArrInstanceError(w, s, err, "The credential could not be read", "Nothing was tried.")
			return
		}
		credential = opened
	}
	if kind == "" || baseURL == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Nothing to test", "give an id or a connection with kind and base_url")
		return
	}
	result := gen.MediaServerTestResult{Libraries: []gen.MediaServerRemoteLibrary{}, Users: []gen.MediaServerRemoteUser{}, Paths: []gen.MediaServerPathCheck{}}
	client, err := s.mediaServerClients(kind, baseURL, username, credential)
	if err == nil {
		err = s.probeMediaServer(r.Context(), client, &result)
	}
	if err != nil {
		reason := explainMediaServer(err)
		result.Error = &reason
	} else {
		result.Ok = true
	}
	if stored != nil {
		message := ""
		if result.Error != nil {
			message = *result.Error
		}
		_ = s.auth.Settings.SetMediaServerResult(r.Context(), stored.ID, s.wall.Now().Unix(), message)
	}
	pairs := derefOr(body.Paths, nil)
	for _, pair := range pairs {
		found := false
		for _, library := range result.Libraries {
			for _, path := range library.Paths {
				found = found || strings.HasPrefix(strings.TrimRight(path, "/")+"/", strings.TrimRight(pair.ServerPrefix, "/")+"/")
			}
		}
		result.Paths = append(result.Paths, gen.MediaServerPathCheck{SolarrPrefix: pair.SolarrPrefix, ServerPrefix: pair.ServerPrefix, Found: found})
	}
	writeJSON(w, s.logger, http.StatusOK, result)
}

func (s *Server) probeMediaServer(ctx context.Context, client mediaserver.Server, result *gen.MediaServerTestResult) error {
	info, err := client.Test(ctx)
	if err != nil {
		return err
	}
	result.Name, result.Version, result.Admin = &info.Name, &info.Version, &info.Admin
	libraries, err := client.Libraries(ctx)
	if err != nil && !errors.Is(err, mediaserver.ErrNotAdmin) {
		return err
	}
	for _, library := range libraries {
		result.Libraries = append(result.Libraries, gen.MediaServerRemoteLibrary{Id: library.ID, Name: library.Name, Type: library.Type, Paths: orEmpty(library.Paths)})
	}
	if lister, ok := client.(mediaserver.UserLister); ok {
		users, err := lister.Users(ctx)
		if err != nil && !errors.Is(err, mediaserver.ErrNotAdmin) {
			return err
		}
		for _, user := range users {
			result.Users = append(result.Users, gen.MediaServerRemoteUser{Id: user.ID, Name: user.Name})
		}
	}
	return nil
}

// explainMediaServer says what went wrong in words a person can act on.
func explainMediaServer(err error) string {
	switch {
	case errors.Is(err, mediaserver.ErrRejected):
		return "The server refused the credential. Check the key, or the user name and password."
	case errors.Is(err, mediaserver.ErrNotAdmin):
		return "The credential works but is not an administrator's, which this needs."
	}
	return httpx.Explain(err)
}

// ListMediaServerLibraries asks the server for its libraries now.
func (s *Server) ListMediaServerLibraries(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.mediaServerClients == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "media server connections are not wired in this build")
		return
	}
	existing, err := s.auth.Settings.GetMediaServer(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	credential, err := s.auth.Settings.DecryptedMediaServerCredential(r.Context(), id)
	if err != nil {
		writeArrInstanceError(w, s, err, "The credential could not be read", "Nothing was tried.")
		return
	}
	client, err := s.mediaServerClients(existing.Kind, existing.BaseURL, existing.Username, credential)
	if err == nil {
		var libraries []mediaserver.Library
		if libraries, err = client.Libraries(r.Context()); err == nil {
			views := make([]gen.MediaServerRemoteLibrary, len(libraries))
			for index, library := range libraries {
				views[index] = gen.MediaServerRemoteLibrary{Id: library.ID, Name: library.Name, Type: library.Type, Paths: orEmpty(library.Paths)}
			}
			writeJSON(w, s.logger, http.StatusOK, views)
			return
		}
	}
	writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The server could not be read", explainMediaServer(err))
}

// SetMediaServerLibraries replaces which Solarr libraries are which libraries of the server.
func (s *Server) SetMediaServerLibraries(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetMediaServerLibrariesJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON array of category_id and server_library_id") {
		return
	}
	existing, err := s.auth.Settings.GetMediaServer(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	kind, _ := mediaserver.KindOf(existing.Kind)
	libraries := make([]settings.MediaServerLibrary, len(body))
	for index, item := range body {
		category, err := access.Category(item.CategoryId, settings.PermissionRead)
		if err != nil {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", fmt.Sprintf("no library %d", item.CategoryId))
			return
		}
		if !kind.Takes(category.MediaType) {
			writeProblem(w, s.logger, http.StatusUnprocessableEntity, TypeBadRequest, "Not a library this server takes",
				fmt.Sprintf("%s holds %s libraries; %q is a %s library", kind.Label, strings.Join(kind.MediaTypes, ", "), category.Name, category.MediaType))
			return
		}
		libraries[index] = settings.MediaServerLibrary{CategoryID: item.CategoryId, ServerLibraryID: item.ServerLibraryId, ServerLibrary: derefOr(item.ServerLibrary, "")}
	}
	if err := s.auth.Settings.SetMediaServerLibraries(r.Context(), id, libraries); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.writeMediaServer(w, r, id, http.StatusOK)
}

// SetMediaServerPaths replaces the path pairs.
func (s *Server) SetMediaServerPaths(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetMediaServerPathsJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON array of solarr_prefix and server_prefix") {
		return
	}
	if _, err := s.auth.Settings.GetMediaServer(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	paths := make([]settings.MediaServerPath, len(body))
	for index, pair := range body {
		if strings.TrimSpace(pair.SolarrPrefix) == "" || strings.TrimSpace(pair.ServerPrefix) == "" {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Empty path", "both sides of a path pair must be set")
			return
		}
		paths[index] = settings.MediaServerPath{SolarrPrefix: pair.SolarrPrefix, ServerPrefix: pair.ServerPrefix}
	}
	if err := s.auth.Settings.SetMediaServerPaths(r.Context(), id, paths); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.writeMediaServer(w, r, id, http.StatusOK)
}

// SetMediaServerUsers replaces the user mapping.
func (s *Server) SetMediaServerUsers(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetMediaServerUsersJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON array of server_user_id, name and counted") {
		return
	}
	if _, err := s.auth.Settings.GetMediaServer(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	users := make([]settings.MediaServerUser, len(body))
	for index, user := range body {
		users[index] = settings.MediaServerUser{ServerUserID: user.ServerUserId, Name: user.Name, UserID: user.UserId, Counted: user.Counted}
	}
	if err := s.auth.Settings.SetMediaServerUsers(r.Context(), id, users); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.writeMediaServer(w, r, id, http.StatusOK)
}

func (s *Server) writeMediaServer(w http.ResponseWriter, r *http.Request, id int64, status int) {
	server, err := s.auth.Settings.GetMediaServer(r.Context(), id)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	view, err := s.mediaServerView(r.Context(), server)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, status, view)
}

func mediaServerRequest(body gen.MediaServerWrite) settings.MediaServerRequest {
	use := settings.MediaServerUse{
		AsMetadata: derefOr(body.AsMetadata, false), PushFiles: derefOr(body.PushFiles, false),
		Announce: derefOr(body.Announce, false), SyncPlayback: derefOr(body.SyncPlayback, false),
	}
	return settings.MediaServerRequest{
		Kind: body.Kind, Name: body.Name, BaseURL: body.BaseUrl, Username: derefOr(body.Username, ""),
		Enabled: derefOr(body.Enabled, true), MediaServerUse: use,
	}
}

func (s *Server) mediaServerView(ctx context.Context, server settings.MediaServer) (gen.MediaServer, error) {
	libraries, err := s.auth.Settings.MediaServerLibraries(ctx, server.ID)
	if err != nil {
		return gen.MediaServer{}, err
	}
	paths, err := s.auth.Settings.MediaServerPaths(ctx, server.ID)
	if err != nil {
		return gen.MediaServer{}, err
	}
	users, err := s.auth.Settings.MediaServerUsers(ctx, server.ID)
	if err != nil {
		return gen.MediaServer{}, err
	}
	view := gen.MediaServer{
		Id: server.ID, Kind: server.Kind, Name: server.Name, BaseUrl: server.BaseURL, Username: server.Username,
		Credential: gen.SecretMask{HasValue: true}, Enabled: server.Enabled, AsMetadata: server.AsMetadata, PushFiles: server.PushFiles,
		Announce: server.Announce, SyncPlayback: server.SyncPlayback,
		Libraries: []gen.MediaServerLibraryMap{}, Paths: []gen.MediaServerPathPair{}, Users: []gen.MediaServerUserMap{},
		CreatedAt: unixTime(server.CreatedAt), UpdatedAt: unixTime(server.UpdatedAt),
	}
	for _, library := range libraries {
		name := library.ServerLibrary
		view.Libraries = append(view.Libraries, gen.MediaServerLibraryMap{CategoryId: library.CategoryID, ServerLibraryId: library.ServerLibraryID, ServerLibrary: &name})
	}
	for _, pair := range paths {
		view.Paths = append(view.Paths, gen.MediaServerPathPair{SolarrPrefix: pair.SolarrPrefix, ServerPrefix: pair.ServerPrefix})
	}
	for _, user := range users {
		view.Users = append(view.Users, gen.MediaServerUserMap{ServerUserId: user.ServerUserID, Name: user.Name, UserId: user.UserID, Counted: user.Counted})
	}
	if server.LastOKAt != nil {
		at := unixTime(*server.LastOKAt)
		view.LastOkAt = &at
	}
	if server.LastError != "" {
		reason := server.LastError
		view.LastError = &reason
	}
	return view, nil
}
