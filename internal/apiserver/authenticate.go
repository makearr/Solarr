package apiserver

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/settings"
)

// APIKeyHeader is the non-interactive credential header.
const APIKeyHeader = "X-Api-Key"

// AuthMode mirrors config's, so apiserver does not depend on config parsing.
type AuthMode string

// The authentication modes of ADR-14.
const (
	// AuthLocal accepts sessions and API keys.
	AuthLocal AuthMode = "local"
	// AuthProxy additionally trusts a header set by a reverse proxy on a
	// trusted network. This is what most self-hosted deployments actually use.
	AuthProxy AuthMode = "proxy"
	// AuthAPIKeyOnly disables interactive login.
	AuthAPIKeyOnly AuthMode = "apikey_only"
	// AuthNone asks for no credential at all (config.AuthNone).
	AuthNone AuthMode = "none"
)

// AuthConfig wires authentication.
type AuthConfig struct {
	Mode     AuthMode
	Sessions *auth.Sessions
	Settings *settings.Cache
	Clock    clock.Clock
	// ProxyHeader carries the username when Mode is proxy.
	ProxyHeader string
	// ProxyNetworks are the only sources whose ProxyHeader is believed.
	ProxyNetworks []netip.Prefix
}

// authenticate resolves a request's identity and attaches an authz.Access.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := s.identify(r)
		if err != nil {
			// Nothing distinguishes an unknown user from a bad password, an
			// expired session or a revoked key.
			writeProblem(w, s.logger, http.StatusUnauthorized, TypeUnauthorized,
				"Unauthorized", "valid credentials are required")
			return
		}

		access := authz.ForUser(s.auth.Settings.Snapshot(), user)
		next.ServeHTTP(w, r.WithContext(authz.WithAccess(r.Context(), access)))
	})
}

var errNoCredential = errors.New("apiserver: no credential presented")

// authMode is the mode in force now, not the one config.yml booted with. Auth
// can be switched off from the interface (ADR-21), so this is read per request
// rather than captured at construction.
func (s *Server) authMode() AuthMode {
	if !s.auth.Settings.Snapshot().AuthRequired() {
		return AuthNone
	}
	return s.auth.Mode
}

func (s *Server) identify(r *http.Request) (settings.User, error) {
	if s.authMode() == AuthNone {
		return s.administrator()
	}
	if user, err := s.identifyBySession(r); err == nil {
		return user, nil
	}
	if user, err := s.identifyByAPIKey(r); err == nil {
		return user, nil
	}
	if user, err := s.identifyByProxy(r); err == nil {
		return user, nil
	}
	return settings.User{}, errNoCredential
}

// administrator is who everyone is when authentication is off. The account is
// real and created at startup, so user_id on a job or a request still names
// somebody and multi-user (ADR-27) needs no special case later.
func (s *Server) administrator() (settings.User, error) {
	for _, user := range s.auth.Settings.Snapshot().Users() {
		if user.Role == settings.RoleAdmin {
			return user, nil
		}
	}
	return settings.User{}, errNoCredential
}

func (s *Server) identifyBySession(r *http.Request) (settings.User, error) {
	if s.authMode() == AuthAPIKeyOnly {
		return settings.User{}, errNoCredential
	}
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return settings.User{}, errNoCredential
	}
	userID, err := s.auth.Sessions.Verify(cookie.Value)
	if err != nil {
		return settings.User{}, err
	}
	user, found := s.auth.Settings.Snapshot().User(userID)
	if !found {
		// A valid signature for a user who has since been deleted.
		return settings.User{}, errNoCredential
	}
	return user, nil
}

func (s *Server) identifyByAPIKey(r *http.Request) (settings.User, error) {
	presented := r.Header.Get(APIKeyHeader)
	if presented == "" {
		presented = bearerToken(r.Header.Get("Authorization"))
	}
	if !auth.LooksLikeAPIKey(presented) {
		return settings.User{}, errNoCredential
	}

	authenticated, err := s.auth.Settings.UserByAPIKeyHash(r.Context(), auth.HashAPIKey(presented))
	if err != nil {
		return settings.User{}, err
	}
	// Bookkeeping, at bulk priority, so it never makes a request wait.
	if err := s.auth.Settings.TouchAPIKey(r.Context(), s.auth.Clock, authenticated.APIKeyID); err != nil {
		s.logger.Debug("could not record api key use", slog.Any("error", err))
	}
	return authenticated.User, nil
}

func (s *Server) identifyByProxy(r *http.Request) (settings.User, error) {
	if s.authMode() != AuthProxy || s.auth.ProxyHeader == "" {
		return settings.User{}, errNoCredential
	}
	if !s.fromTrustedNetwork(r.RemoteAddr) {
		// The header is only evidence when it cannot have come from the client.
		return settings.User{}, errNoCredential
	}

	username := strings.TrimSpace(r.Header.Get(s.auth.ProxyHeader))
	if username == "" {
		return settings.User{}, errNoCredential
	}
	user, found := s.auth.Settings.Snapshot().UserByUsername(username)
	if !found {
		return settings.User{}, errNoCredential
	}
	return user, nil
}

// fromTrustedNetwork reports whether the immediate peer is a configured proxy.
func (s *Server) fromTrustedNetwork(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	peer = peer.Unmap()

	for _, network := range s.auth.ProxyNetworks {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

func bearerToken(header string) string {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return ""
	}
	return strings.TrimSpace(token)
}
