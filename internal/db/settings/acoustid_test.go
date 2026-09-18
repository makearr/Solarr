package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func cacheWithSecrets(t *testing.T) (*settings.Cache, *clock.Fake) {
	t.Helper()
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	return cache, fake
}

// The key is a credential, so it is never written in the clear -- not even
// into settings.db, which is already the only place secrets may live (ADR-14).
func TestAcoustIDAPIKey_IsStoredSealed(t *testing.T) {
	cache, fake := cacheWithSecrets(t)
	const key = "a-real-acoustid-key"

	if err := cache.SetAcoustIDAPIKey(t.Context(), fake, secrets.Secret(key)); err != nil {
		t.Fatalf("SetAcoustIDAPIKey: %v", err)
	}
	opened, err := cache.AcoustIDAPIKey()
	if err != nil {
		t.Fatalf("AcoustIDAPIKey: %v", err)
	}
	if string(opened) != key {
		t.Errorf("opened = %q, want the key back", opened)
	}

	// That it is stored sealed rather than in the clear is asserted in
	// acoustid_sealed_test.go, which can see the stored value from inside the
	// package.
}

// No key is the disabled state, not an error (D12).
func TestAcoustIDAPIKey_AbsentIsEmptyNotAnError(t *testing.T) {
	cache, _ := cacheWithSecrets(t)

	if cache.Snapshot().AcoustIDConfigured() {
		t.Error("AcoustIDConfigured() = true with nothing stored")
	}
	key, err := cache.AcoustIDAPIKey()
	if err != nil {
		t.Fatalf("AcoustIDAPIKey: %v", err)
	}
	if key != "" {
		t.Errorf("key = %q, want empty", key)
	}
}

func TestAcoustIDAPIKey_CanBeCleared(t *testing.T) {
	cache, fake := cacheWithSecrets(t)
	if err := cache.SetAcoustIDAPIKey(t.Context(), fake, "some-key"); err != nil {
		t.Fatalf("SetAcoustIDAPIKey: %v", err)
	}
	if !cache.Snapshot().AcoustIDConfigured() {
		t.Fatal("AcoustIDConfigured() = false after storing one")
	}
	if err := cache.SetAcoustIDAPIKey(t.Context(), fake, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if cache.Snapshot().AcoustIDConfigured() {
		t.Error("AcoustIDConfigured() = true after clearing")
	}
}

// Without a cipher there is nowhere safe to put it, so storing is refused
// rather than falling back to plain text.
func TestAcoustIDAPIKey_RefusedWithoutACipher(t *testing.T) {
	cache, fake := newCache(t) // EnableSecrets deliberately not called.

	if err := cache.SetAcoustIDAPIKey(t.Context(), fake, "a-key"); err == nil {
		t.Error("storing a key without a cipher succeeded, want a refusal")
	}
}
