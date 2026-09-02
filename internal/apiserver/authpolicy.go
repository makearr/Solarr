package apiserver

import (
	"encoding/json"
	"net/http"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/settings"
)

// OpenAccessUsername owns an install that asks for no credential.
const OpenAccessUsername = "administrator"

// GetAuthPolicy is unauthenticated: an instance that requires no login has
// nobody to authenticate, and the interface still has to discover that.
func (s *Server) GetAuthPolicy(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, s.authPolicyView())
}

// SetAuthPolicy turns the login requirement on or off. Administrators only,
// except before the first account exists: during setup there is nobody to be an
// administrator, and refusing there would make the choice unreachable exactly
// where it is offered.
func (s *Server) SetAuthPolicy(w http.ResponseWriter, r *http.Request) {
	// Identified here rather than by middleware: the route is open so that the
	// first-run wizard, where there is nobody to be an administrator, can reach
	// it. Once an account exists the check is the same one everywhere else.
	claimed := len(s.auth.Settings.Snapshot().Users()) > 0
	if claimed {
		user, err := s.identify(r)
		if err != nil {
			writeProblem(w, s.logger, http.StatusUnauthorized, TypeUnauthorized,
				"Unauthorized", "valid credentials are required")
			return
		}
		if user.Role != settings.RoleAdmin {
			writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden,
				"Forbidden", "only an administrator may change how access works")
			return
		}
	}

	var request struct {
		AuthRequired *bool  `json:"auth_required"`
		Username     string `json:"username"`
		Password     string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with auth_required")
		return
	}
	if request.AuthRequired == nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "auth_required is required")
		return
	}

	// Requiring a login that nobody can satisfy locks the instance out of its own
	// interface, which ADR-44 exists to prevent. An account existing is not
	// enough: the one created when authentication was switched off carries no
	// password, so it cannot be signed in as. A proxy or API key install has its
	// credential elsewhere and is exempt.
	if *request.AuthRequired && s.auth.Mode == AuthLocal && !s.someoneCanSignIn() {
		if request.Password == "" {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Nobody could sign in",
				"no account has a password yet, so send a username and password to set one "+
					"with this change; without it the interface locks itself out")
			return
		}
		if err := s.giveCredential(r, request.Username, request.Password); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}

	if err := s.auth.Settings.SetAuthRequired(r.Context(), s.auth.Clock, *request.AuthRequired); err != nil {
		writeError(w, s.logger, err)
		return
	}

	// Turning authentication off before any account exists still needs one: it is
	// who every request is served as, and it gives user_id on a job something to
	// name. Passwordless, which settings.db already models for an account that
	// does not authenticate by password.
	if !*request.AuthRequired && !claimed {
		created, err := s.auth.Settings.CreateUser(r.Context(), s.auth.Clock,
			OpenAccessUsername, "", settings.RoleAdmin)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		s.logger.WarnContext(r.Context(),
			"created a passwordless administrator because authentication was disabled",
			"username", created.Username)
	}

	// Loud on purpose: an instance that stops asking for credentials should say
	// so somewhere durable, not only in whichever browser tab did it.
	if *request.AuthRequired {
		s.logger.WarnContext(r.Context(), "authentication is now required")
	} else {
		s.logger.WarnContext(r.Context(),
			"authentication has been disabled; every request is served as the administrator")
	}
	writeJSON(w, s.logger, http.StatusOK, s.authPolicyView())
}

func (s *Server) authPolicyView() gen.AuthPolicy {
	// The mode config.yml carries is what a login goes back to. `none` there was
	// only ever a way of saying "not required", which the policy now says.
	configured := s.auth.Mode
	if configured == AuthNone {
		configured = AuthLocal
	}
	view := gen.AuthPolicy{
		AuthRequired:   s.auth.Settings.Snapshot().AuthRequired(),
		ConfiguredMode: gen.AuthPolicyConfiguredMode(configured),
		CanSignIn:      s.someoneCanSignIn(),
	}
	for _, user := range s.auth.Settings.Snapshot().Users() {
		if user.Role == settings.RoleAdmin {
			name := user.Username
			view.Administrator = &name
			break
		}
	}
	return view
}

// someoneCanSignIn reports whether any account has a password to present.
func (s *Server) someoneCanSignIn() bool {
	for _, user := range s.auth.Settings.Snapshot().Users() {
		if user.HasPassword {
			return true
		}
	}
	return false
}

// giveCredential puts a password on the account a login will use. The passwordless
// administrator an open-access install created is renamed rather than joined by a
// second one, so turning authentication on does not leave an unusable account behind.
func (s *Server) giveCredential(r *http.Request, username, password string) error {
	hashed, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	// The existing administrator gains the password rather than being joined by a
	// second account. Renaming is not offered here: the account keeps the name it
	// has, and the interface shows which one that is.
	for _, user := range s.auth.Settings.Snapshot().Users() {
		if user.Role == settings.RoleAdmin {
			return s.auth.Settings.SetPassword(r.Context(), user.ID, hashed)
		}
	}

	name := username
	if name == "" {
		name = OpenAccessUsername
	}
	_, err = s.auth.Settings.CreateUser(r.Context(), s.auth.Clock, name, hashed, settings.RoleAdmin)
	return err
}
