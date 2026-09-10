package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/api/auth"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

const password = "correct horse battery staple"

func TestPassword_RoundTrips(t *testing.T) {
	encoded, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if strings.Contains(encoded, password) {
		t.Fatal("the encoded hash contains the password")
	}
	if err := auth.VerifyPassword(password, encoded); err != nil {
		t.Errorf("VerifyPassword = %v, want nil", err)
	}
}

func TestPassword_RejectsAWrongPassword(t *testing.T) {
	encoded, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := auth.VerifyPassword("wrong", encoded); !errors.Is(err, auth.ErrPasswordMismatch) {
		t.Errorf("VerifyPassword = %v, want ErrPasswordMismatch", err)
	}
}

// A shared salt would let one precomputed table break every account at once.
func TestPassword_SaltsEachHash(t *testing.T) {
	first, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Fatal("two hashes of one password are identical")
	}
	for _, encoded := range []string{first, second} {
		if err := auth.VerifyPassword(password, encoded); err != nil {
			t.Errorf("VerifyPassword: %v", err)
		}
	}
}

// The parameters travel with the hash, so raising them later does not
// invalidate stored passwords.
func TestPassword_VerifiesAgainstWeakerStoredParameters(t *testing.T) {
	// m=8,t=1,p=1 over the same password; deliberately below current settings.
	weaker := "$argon2id$v=19$m=8,t=1,p=1$" +
		"YWJjZGVmZ2hpamtsbW5vcA$" + weakKey(t)

	if err := auth.VerifyPassword(password, weaker); err != nil {
		t.Errorf("VerifyPassword against older parameters = %v, want nil", err)
	}
	if !auth.NeedsRehash(weaker) {
		t.Error("NeedsRehash = false for weaker parameters; the upgrade would never happen")
	}
}

func TestPassword_CurrentParametersDoNotNeedRehashing(t *testing.T) {
	encoded, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if auth.NeedsRehash(encoded) {
		t.Error("NeedsRehash = true for a freshly made hash")
	}
}

func TestPassword_RejectsMalformedHashes(t *testing.T) {
	cases := []string{
		"", "not-a-hash", "$argon2i$v=19$m=8,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=99$m=8,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdA",
	}
	for _, encoded := range cases {
		if err := auth.VerifyPassword(password, encoded); !errors.Is(err, auth.ErrMalformedHash) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrMalformedHash", encoded, err)
		}
	}
}

func TestAPIKey_HashIsStableAndTheKeyIsNotRecoverable(t *testing.T) {
	key, hash, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}

	if !auth.LooksLikeAPIKey(key.Reveal()) {
		t.Errorf("generated key %q is not recognisable as ours", key.Reveal())
	}
	if strings.Contains(hash, key.Reveal()) {
		t.Fatal("the stored hash contains the key")
	}
	if auth.HashAPIKey(key.Reveal()) != hash {
		t.Error("hashing the key again produced a different hash")
	}
	// The key is a Secret, so it cannot reach a log by accident.
	if strings.Contains(key.String(), "mnx_") {
		t.Error("the key rendered itself instead of redacting")
	}
}

func TestAPIKey_KeysAreDistinct(t *testing.T) {
	seen := make(map[string]bool, 64)
	for range 64 {
		_, hash, err := auth.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if seen[hash] {
			t.Fatal("two generated keys collided")
		}
		seen[hash] = true
	}
}

func TestLooksLikeAPIKey(t *testing.T) {
	cases := map[string]bool{
		"":                    false,
		"mnx_":                false,
		"mnx_short":           false,
		"password123":         false,
		"Bearer mnx_abcdefgh": false,
	}
	for candidate, want := range cases {
		if got := auth.LooksLikeAPIKey(candidate); got != want {
			t.Errorf("LooksLikeAPIKey(%q) = %v, want %v", candidate, got, want)
		}
	}
}

func newSessions(t *testing.T) (*auth.Sessions, *clock.Fake) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	return auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), fake, time.Hour), fake
}

func TestSession_RoundTrips(t *testing.T) {
	sessions, _ := newSessions(t)

	token := sessions.Issue(42)
	userID, err := sessions.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if userID != 42 {
		t.Errorf("Verify = %d, want 42", userID)
	}
}

func TestSession_RejectsTampering(t *testing.T) {
	sessions, _ := newSessions(t)
	token := sessions.Issue(42)

	// Claiming to be user 1 with user 42's signature.
	separator := strings.Index(token, ".")
	if separator < 0 {
		t.Fatalf("issued token %q has no separator", token)
	}
	forged := "1" + token[separator:]
	if _, err := sessions.Verify(forged); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("Verify of a forged token = %v, want ErrInvalidSession", err)
	}
}

func TestSession_RejectsAnotherKeysToken(t *testing.T) {
	sessions, fake := newSessions(t)
	other := auth.NewSessions([]byte("ffffffffffffffffffffffffffffffff"), fake, time.Hour)

	// Rotating the encryption key is the revocation path for signed sessions,
	// so a token from a different key must not verify.
	if _, err := sessions.Verify(other.Issue(42)); !errors.Is(err, auth.ErrInvalidSession) {
		t.Errorf("Verify of another key's token = %v, want ErrInvalidSession", err)
	}
}

func TestSession_Expires(t *testing.T) {
	sessions, fake := newSessions(t)
	token := sessions.Issue(42)

	fake.Advance(time.Hour - time.Second)
	if _, err := sessions.Verify(token); err != nil {
		t.Fatalf("Verify just before expiry = %v, want nil", err)
	}

	fake.Advance(2 * time.Second)
	if _, err := sessions.Verify(token); !errors.Is(err, auth.ErrSessionExpired) {
		t.Errorf("Verify after expiry = %v, want ErrSessionExpired", err)
	}
}

func TestSession_RejectsGarbage(t *testing.T) {
	sessions, _ := newSessions(t)
	for _, token := range []string{"", "nonsense", "1.2", "1.2.3.4"} {
		if _, err := sessions.Verify(token); err == nil {
			t.Errorf("Verify(%q) = nil, want an error", token)
		}
	}
}

// weakKey computes the argon2id key for the deliberately weak parameters used
// above, so the fixture cannot drift from the algorithm.
func weakKey(t *testing.T) string {
	t.Helper()
	return auth.WeakFixtureKeyForTest(password, "abcdefghijklmnop")
}
