package apiserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// maxUserBody bounds what one request may make the server read; an account's grants list every library.
const maxUserBody = 64 << 10

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
		Username string              `json:"username"`
		Password string              `json:"password"`
		Role     string              `json:"role"`
		Access   []gen.CategoryGrant `json:"access"`
		Limits   *gen.UserLimits     `json:"limits"`
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

	account := settings.NewAccount{
		Username: username, PasswordHash: hashed, Role: role, Limits: s.auth.Settings.DefaultLimits(role),
		Access: make(map[int64]settings.Permission, len(request.Access)),
	}
	if request.Limits != nil {
		account.Limits = limitsFrom(*request.Limits)
	}
	for _, grant := range request.Access {
		account.Access[grant.CategoryId] = settings.Permission(grant.Permission)
	}
	created, err := s.auth.Settings.CreateAccount(r.Context(), s.auth.Clock, account)
	if err != nil {
		s.writeUserError(w, err)
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
		Role     *string         `json:"role"`
		Password *string         `json:"password"`
		Limits   *gen.UserLimits `json:"limits"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with role or password")
		return
	}

	// Every refusal comes before the first write, so a 400 changes nothing.
	if request.Password != nil && *request.Password == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a password is required")
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

	if request.Limits != nil {
		if err := s.auth.Settings.SetUserLimits(r.Context(), id, limitsFrom(*request.Limits)); err != nil {
			s.writeUserError(w, err)
			return
		}
	}

	if request.Password != nil {
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
	if request.Password != nil {
		s.keepSignedIn(w, r, user)
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
	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"The account has problems", problems.Report("the account", "Nothing was changed."))
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
		Tier:        gen.Tier(user.Tier),
		Limits: gen.UserLimits{
			TierDefault: gen.Tier(user.Limits.TierDefault), TierMax: gen.Tier(user.Limits.TierMax),
			NewLibraryPermission: gen.Permission(user.Limits.NewLibraryPermission),
		},
	}
}

func limitsFrom(limits gen.UserLimits) settings.UserLimits {
	return settings.UserLimits{
		TierDefault: settings.Tier(limits.TierDefault), TierMax: settings.Tier(limits.TierMax),
		NewLibraryPermission: settings.Permission(limits.NewLibraryPermission),
	}
}

func (s *Server) GetUserDefaults(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.UserDefaults{
		NewLibraryPermission: gen.Permission(s.auth.Settings.Snapshot().NewLibraryPermission()),
	})
}

func (s *Server) SetUserDefaults(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var request gen.UserDefaults
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with new_library_permission")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyUsersNewLibraryPermission); err != nil {
		writeError(w, s.logger, err)
		return
	}
	permission := settings.Permission(request.NewLibraryPermission)
	if err := s.auth.Settings.SetNewLibraryPermission(r.Context(), s.auth.Clock, permission); err != nil {
		s.writeUserError(w, err)
		return
	}
	s.GetUserDefaults(w, r)
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
func (s *Server) routeUser(handler func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an account id must be a number")
			return
		}
		handler(w, r, id)
	}
}

// keepSignedIn gives the browser that changed its own password a session under the new one: every other session of
// the account ended with the old password.
func (s *Server) keepSignedIn(w http.ResponseWriter, r *http.Request, user settings.User) {
	access, err := authz.FromContext(r.Context())
	if err != nil || access.UserID() != user.ID {
		return
	}
	if _, err := r.Cookie(auth.SessionCookieName); err != nil {
		return
	}
	http.SetCookie(w, s.sessionCookie(s.auth.Sessions.Issue(user.ID, user.SessionStamp), int(s.auth.Sessions.TTL().Seconds()), r))
}
