package apiserver

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
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
	// Registry is ADR-26's settings registry (todo 4.14), reused here for
	// every scalar setting Settings.Cache writes directly -- auth, network
	// and import policy -- the same direct-Cache shape those handlers
	// already share.
	Registry *settings.Registry
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
	userID, stamp, err := s.auth.Sessions.Verify(cookie.Value)
	if err != nil {
		return settings.User{}, err
	}
	user, found := s.auth.Settings.Snapshot().User(userID)
	if !found || subtle.ConstantTimeCompare([]byte(stamp), []byte(user.SessionStamp)) != 1 {
		// A user since deleted, or a session from before the password last changed.
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
	peer, ok := peerAddress(remoteAddr)
	if !ok {
		return false
	}
	for _, network := range s.auth.ProxyNetworks {
		if network.Contains(peer) {
			return true
		}
	}
	return false
}

// peerAddress parses the socket's own remote address, unmapped to its plain
// v4 or v6 form so it compares directly against a configured CIDR.
func peerAddress(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return peer.Unmap(), true
}

// resolvedClientAddress is the socket peer, promoted to X-Forwarded-For's
// claim only from a trusted proxy -- else the header is decoration (Inv. 89).
func (s *Server) resolvedClientAddress(r *http.Request) netip.Addr {
	peer, ok := peerAddress(r.RemoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if !s.fromTrustedNetwork(r.RemoteAddr) {
		return peer
	}

	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded == "" {
		return peer
	}
	first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
	if claimed, err := netip.ParseAddr(first); err == nil {
		return claimed.Unmap()
	}
	return peer
}

func bearerToken(header string) string {
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found {
		return ""
	}
	return strings.TrimSpace(token)
}
