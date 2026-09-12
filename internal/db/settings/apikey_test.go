package settings_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
)

// CreateAPIKey, ListAPIKeys and DeleteAPIKey existed and were exercised only
// indirectly (through UserByAPIKeyHash's own callers) before the HTTP
// surface reached them -- these are the engine layer's own dedicated tests.

func TestCreateAPIKey_RoundTripsAndMarksTheUserOwner(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	created, err := cache.CreateAPIKey(t.Context(), fake, user.ID, "scripts", "keyhash")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if created.UserID != user.ID || created.Name != "scripts" {
		t.Fatalf("created = %+v, want UserID=%d Name=scripts", created, user.ID)
	}

	found, err := cache.UserByAPIKeyHash(t.Context(), "keyhash")
	if err != nil {
		t.Fatalf("UserByAPIKeyHash: %v", err)
	}
	if found.User.ID != user.ID || found.APIKeyID != created.ID {
		t.Errorf("found = %+v, want user %d key %d", found, user.ID, created.ID)
	}
}

func TestCreateAPIKey_RefusesAMissingUser(t *testing.T) {
	cache, fake := newCache(t)
	if _, err := cache.CreateAPIKey(t.Context(), fake, 404, "scripts", "keyhash"); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("CreateAPIKey for a missing user = %v, want ErrNotFound", err)
	}
}

func TestListAPIKeys_OnlyTheOwnersOwn(t *testing.T) {
	cache, fake := newCache(t)
	owner, err := cache.CreateUser(t.Context(), fake, "owner", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(owner): %v", err)
	}
	other, err := cache.CreateUser(t.Context(), fake, "other", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser(other): %v", err)
	}
	if _, err := cache.CreateAPIKey(t.Context(), fake, owner.ID, "owner-key", "hash-a"); err != nil {
		t.Fatalf("CreateAPIKey(owner): %v", err)
	}
	if _, err := cache.CreateAPIKey(t.Context(), fake, other.ID, "other-key", "hash-b"); err != nil {
		t.Fatalf("CreateAPIKey(other): %v", err)
	}

	keys, err := cache.ListAPIKeys(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "owner-key" {
		t.Fatalf("ListAPIKeys(owner) = %+v, want exactly [owner-key]", keys)
	}
}

// The real guard: DeleteAPIKey's own query deletes by a bare id with no
// ownership check, so the ownership check inside DeleteAPIKey itself is
// what stops one user from revoking another's key by guessing its id.
func TestDeleteAPIKey_RefusesAnotherUsersKey(t *testing.T) {
	cache, fake := newCache(t)
	owner, err := cache.CreateUser(t.Context(), fake, "owner", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(owner): %v", err)
	}
	other, err := cache.CreateUser(t.Context(), fake, "other", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser(other): %v", err)
	}
	key, err := cache.CreateAPIKey(t.Context(), fake, owner.ID, "owner-key", "hash-a")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if err := cache.DeleteAPIKey(t.Context(), other.ID, key.ID); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("DeleteAPIKey by a different user = %v, want ErrNotFound", err)
	}

	keys, err := cache.ListAPIKeys(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 {
		t.Error("the owner's key was removed despite the refusal")
	}
}

func TestDeleteAPIKey_RemovesTheOwnersOwnKey(t *testing.T) {
	cache, fake := newCache(t)
	owner, err := cache.CreateUser(t.Context(), fake, "owner", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := cache.CreateAPIKey(t.Context(), fake, owner.ID, "owner-key", "hash-a")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	if err := cache.DeleteAPIKey(t.Context(), owner.ID, key.ID); err != nil {
		t.Fatalf("DeleteAPIKey: %v", err)
	}

	keys, err := cache.ListAPIKeys(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %+v, want none after deletion", keys)
	}
}

func TestTouchAPIKey_RecordsLastUsed(t *testing.T) {
	cache, fake := newCache(t)
	owner, err := cache.CreateUser(t.Context(), fake, "owner", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	key, err := cache.CreateAPIKey(t.Context(), fake, owner.ID, "owner-key", "hash-a")
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	if !key.LastUsedAt.IsZero() {
		t.Fatal("a freshly created key already has a last-used time")
	}

	if err := cache.TouchAPIKey(t.Context(), fake, key.ID); err != nil {
		t.Fatalf("TouchAPIKey: %v", err)
	}

	keys, err := cache.ListAPIKeys(t.Context(), owner.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].LastUsedAt.IsZero() {
		t.Fatalf("keys = %+v, want LastUsedAt set after Touch", keys)
	}
}

func TestUserByAPIKeyHash_RefusesAnUnknownHash(t *testing.T) {
	cache, _ := newCache(t)
	if _, err := cache.UserByAPIKeyHash(t.Context(), "no-such-hash"); !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("UserByAPIKeyHash for an unknown hash = %v, want ErrNotFound", err)
	}
}
