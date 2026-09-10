package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/auth"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

// maxUserBody bounds what one request may make the server read.
const maxUserBody = 4 << 10

func (s *Server) ListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	snapshot := s.auth.Settings.Snapshot()
	views := make([]gen.UserAccount, 0, len(snapshot.Users()))
	for _, user := range snapshot.Users() {
		views = append(views, s.userView(user))
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with username, password and role")
		return
	}

	username := strings.TrimSpace(request.Username)
	if username == "" || request.Password == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a username and password are required")
		return
	}
	role, ok := parseRole(request.Role)
	if !ok {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "role must be admin, operator or readonly")
		return
	}

	hashed, err := auth.HashPassword(request.Password)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	created, err := s.auth.Settings.CreateUser(r.Context(), s.auth.Clock, username, hashed, role)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.logger.InfoContext(r.Context(), "account created",
		"username", created.Username, "role", string(created.Role))
	writeJSON(w, s.logger, http.StatusCreated, s.userView(created))
}

func (s *Server) UpdateUser(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var request struct {
		Role     *string `json:"role"`
		Password *string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with role or password")
		return
	}

	if request.Role != nil {
		role, ok := parseRole(*request.Role)
		if !ok {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "role must be admin, operator or readonly")
			return
		}
		if err := s.auth.Settings.SetRole(r.Context(), id, role); err != nil {
			s.writeUserError(w, err)
			return
		}
	}

	if request.Password != nil {
		if *request.Password == "" {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "a password is required")
			return
		}
		hashed, err := auth.HashPassword(*request.Password)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		if err := s.auth.Settings.SetPassword(r.Context(), id, hashed); err != nil {
			s.writeUserError(w, err)
			return
		}
	}

	user, found := s.auth.Settings.Snapshot().User(id)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such account")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.userView(user))
}

func (s *Server) DeleteUser(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteUser(r.Context(), id); err != nil {
		s.writeUserError(w, err)
		return
	}
	s.logger.InfoContext(r.Context(), "account removed", "user_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) SetUserAccess(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}

	var request struct {
		CategoryID int64  `json:"category_id"`
		Permission string `json:"permission"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with category_id and permission")
		return
	}

	permission, ok := parsePermission(request.Permission)
	if !ok {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "permission must be none, read, request or manage")
		return
	}

	// An absent grant already denies, so "none" removes the row rather than
	// storing a denial that means the same thing.
	var err error
	if permission == settings.PermissionNone {
		err = s.auth.Settings.RevokeCategoryAccess(r.Context(), id, request.CategoryID)
	} else {
		err = s.auth.Settings.SetCategoryAccess(r.Context(), id, request.CategoryID, permission)
	}
	if err != nil {
		s.writeUserError(w, err)
		return
	}

	user, found := s.auth.Settings.Snapshot().User(id)
	if !found {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such account")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.userView(user))
}

// writeUserError maps the two refusals a caller can act on, so removing the last
// administrator reads as a rule rather than as a server fault.
func (s *Server) writeUserError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrLastAdministrator):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"That would leave no administrator", err.Error())
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", err.Error())
	default:
		writeError(w, s.logger, err)
	}
}

func (s *Server) userView(user settings.User) gen.UserAccount {
	snapshot := s.auth.Settings.Snapshot()
	grants := make([]gen.CategoryGrant, 0)
	for _, category := range snapshot.Categories() {
		permission := snapshot.Permission(user.ID, category.ID)
		if permission == settings.PermissionNone {
			continue
		}
		grants = append(grants, gen.CategoryGrant{
			CategoryId: category.ID,
			Permission: gen.Permission(permission),
		})
	}
	return gen.UserAccount{
		Id:          user.ID,
		Username:    user.Username,
		Role:        gen.Role(user.Role),
		HasPassword: user.HasPassword,
		CreatedAt:   user.CreatedAt,
		Access:      grants,
	}
}

func parseRole(value string) (settings.Role, bool) {
	switch settings.Role(value) {
	case settings.RoleAdmin, settings.RoleOperator, settings.RoleReadonly:
		return settings.Role(value), true
	}
	return "", false
}

func parsePermission(value string) (settings.Permission, bool) {
	switch settings.Permission(value) {
	case settings.PermissionNone, settings.PermissionRead,
		settings.PermissionRequest, settings.PermissionManage:
		return settings.Permission(value), true
	}
	return "", false
}

// routeUser parses the id path parameter once, so each handler receives it
// already typed and a malformed one answers the same way everywhere.
func routeUser(handler func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			http.Error(w, "an account id must be a number", http.StatusBadRequest)
			return
		}
		handler(w, r, id)
	}
}
