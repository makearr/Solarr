package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/clock"
)

var (
	// ErrInvalidSession reports a cookie that was not issued by this key, or was tampered with.
	ErrInvalidSession = errors.New("auth: session is not valid")

	// ErrSessionExpired reports a session past its lifetime.
	ErrSessionExpired = errors.New("auth: session has expired")
)

// SessionCookieName is the cookie the browser carries.
const SessionCookieName = "solarr_session"

// DefaultSessionTTL is how long a login lasts.
const DefaultSessionTTL = 30 * 24 * time.Hour

// Sessions issues and verifies signed session tokens.
type Sessions struct {
	key   []byte
	clock clock.Clock
	ttl   time.Duration
}

// NewSessions derives a signing key from the encryption key.
func NewSessions(encryptionKey []byte, cl clock.Clock, ttl time.Duration) *Sessions {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	derived := hmac.New(sha256.New, encryptionKey)
	derived.Write([]byte("solarr:session:v1"))
	return &Sessions{key: derived.Sum(nil), clock: cl, ttl: ttl}
}

// Issue returns a signed token for a user.
func (s *Sessions) Issue(userID int64) string {
	expires := s.clock.Now().Add(s.ttl).Unix()
	claims := fmt.Sprintf("%d.%d", userID, expires)
	return claims + "." + s.sign(claims)
}

// TTL reports how long an issued session lasts, for the cookie's Max-Age.
func (s *Sessions) TTL() time.Duration { return s.ttl }

// Verify checks a token's signature and expiry, returning the user it names.
func (s *Sessions) Verify(token string) (int64, error) {
	claims, signature := reverseCut(token)
	if signature == "" {
		return 0, ErrInvalidSession
	}
	if !hmac.Equal([]byte(signature), []byte(s.sign(claims))) {
		return 0, ErrInvalidSession
	}

	rawUser, rawExpiry, found := strings.Cut(claims, ".")
	if !found {
		return 0, ErrInvalidSession
	}
	userID, err := strconv.ParseInt(rawUser, 10, 64)
	if err != nil {
		return 0, ErrInvalidSession
	}
	expires, err := strconv.ParseInt(rawExpiry, 10, 64)
	if err != nil {
		return 0, ErrInvalidSession
	}
	if s.clock.Now().Unix() >= expires {
		return 0, ErrSessionExpired
	}
	return userID, nil
}

func (s *Sessions) sign(claims string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(claims))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// reverseCut splits at the last separator, so the claims keep their own dot.
func reverseCut(token string) (string, string) {
	index := strings.LastIndex(token, ".")
	if index < 0 {
		return token, ""
	}
	return token[:index], token[index+1:]
}
