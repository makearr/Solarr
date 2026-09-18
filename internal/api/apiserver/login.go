package apiserver

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/db/settings"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Remember keeps the session across browser restarts. It changes the cookie,
	// not the server-side lifetime: a session record expires on its own schedule
	// either way, so forgetting to tick this cannot extend anything.
	Remember bool `json:"remember"`
}

// Login exchanges a username and password for a session cookie.
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	if s.authMode() == AuthAPIKeyOnly {
		writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden,
			"Login disabled", "this instance is configured for API keys only")
		return
	}
	if s.authMode() == AuthNone {
		writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden,
			"Login disabled", "this instance runs without authentication")
		return
	}

	var request loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with username and password")
		return
	}

	user, err := s.verifyPassword(r, strings.TrimSpace(request.Username), request.Password)
	if err != nil {
		s.logger.Info("failed login", slog.String("username", request.Username))
		writeProblem(w, s.logger, http.StatusUnauthorized, TypeUnauthorized,
			"Unauthorized", "the username or password is incorrect")
		return
	}

	// A session cookie (no Max-Age) dies with the browser, which is what an
	// unticked "stay signed in" means everywhere else.
	maxAge := 0
	if request.Remember {
		maxAge = int(s.auth.Sessions.TTL().Seconds())
	}
	http.SetCookie(w, s.sessionCookie(s.auth.Sessions.Issue(user.ID), maxAge, r))
	writeJSON(w, s.logger, http.StatusOK, map[string]any{
		"user_id": user.ID, "username": user.Username, "role": string(user.Role),
	})
}

// maxLoginBody bounds what an unauthenticated caller can make the server read.
const maxLoginBody = 4 << 10

func (s *Server) verifyPassword(r *http.Request, username, password string) (settings.User, error) {
	user, found := s.auth.Settings.Snapshot().UserByUsername(username)
	if !found {
		// Still hash something, so a missing user and a wrong password take
		// comparable time and cannot be told apart by a stopwatch.
		_ = auth.VerifyPassword(password, decoyHash())
		return settings.User{}, errNoCredential
	}

	stored, err := s.auth.Settings.PasswordHash(r.Context(), username)
	if err != nil || stored == "" {
		_ = auth.VerifyPassword(password, decoyHash())
		return settings.User{}, errNoCredential
	}
	if err := auth.VerifyPassword(password, stored); err != nil {
		return settings.User{}, err
	}
	return user, nil
}

// decoyHash is a genuine argon2id hash of a random value, so verifying against it costs the
// same as verifying a real password.
var decoyHash = sync.OnceValue(func() string {
	filler := make([]byte, 32)
	if _, err := rand.Read(filler); err != nil {
		// Falling back to a fixed value still costs a full argon2id run, which
		// is the only property that matters here.
		return mustHash("solarr-decoy")
	}
	return mustHash(string(filler))
})

func mustHash(value string) string {
	hashed, err := auth.HashPassword(value)
	if err != nil {
		panic("apiserver: cannot hash the login decoy: " + err.Error())
	}
	return hashed
}

// Logout clears the session cookie.
func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.sessionCookie("", -1, r))
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"status": "signed-out"})
}

// sessionCookie builds the cookie consistently for setting and clearing.
func (s *Server) sessionCookie(value string, maxAge int, r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:  auth.SessionCookieName,
		Value: value,
		Path:  "/",
		// The session token is only ever read by the server, so JavaScript has
		// no reason to see it and cross-site scripting has no way to steal it.
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure only over TLS: a self-hosted instance on plain HTTP inside a
		// LAN would otherwise silently fail to log in.
		Secure: r.TLS != nil,
		// Zero omits Max-Age entirely, making it a session cookie; -1 expires it.
		MaxAge: maxAge,
	}
}
