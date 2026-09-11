package settings_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

func testCipherKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

func TestCreateArrInstance_RoundTripsThroughSealAndOpen(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}
	if created.App != settings.ArrAppLidarr || created.Name != "Home Lidarr" {
		t.Errorf("created = %+v, want app=lidarr name=%q", created, "Home Lidarr")
	}

	opened, err := cache.DecryptedAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedAPIKey: %v", err)
	}
	if opened.Reveal() != "a-real-api-key" {
		t.Errorf("DecryptedAPIKey = %q, want the original key back", opened.Reveal())
	}
}

// The api_key column must never hold the raw key -- CreateArrInstance seals
// it, so reading the row back through anything other than DecryptedAPIKey
// (which this test does not call) must never see the plaintext.
func TestCreateArrInstance_RefusesWithoutSecretsEnabled(t *testing.T) {
	cache, fake := newCache(t)
	// EnableSecrets deliberately not called.

	_, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if !errors.Is(err, settings.ErrSecretsUnavailable) {
		t.Errorf("err = %v, want ErrSecretsUnavailable", err)
	}
}

func TestCreateArrInstance_RefusesAnEmptyAPIKey(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	_, err = cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret(""), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateArrInstance succeeded with an empty api key")
	}
}

func TestSetArrInstanceAPIKey_ReplacesTheSealedValue(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret("old-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}

	if err := cache.SetArrInstanceAPIKey(t.Context(), created.ID, secrets.Secret("new-key"), fake.Now().Unix()); err != nil {
		t.Fatalf("SetArrInstanceAPIKey: %v", err)
	}
	opened, err := cache.DecryptedAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedAPIKey: %v", err)
	}
	if opened.Reveal() != "new-key" {
		t.Errorf("DecryptedAPIKey = %q, want %q", opened.Reveal(), "new-key")
	}
}

func TestUpdateArrInstance_NeverTouchesTheAPIKey(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret("stays-the-same"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}

	updated, err := cache.UpdateArrInstance(t.Context(), created.ID, settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Renamed Lidarr", BaseURL: "http://lidarr.local:8686", Enabled: true,
	}, fake.Now().Unix())
	if err != nil {
		t.Fatalf("UpdateArrInstance: %v", err)
	}
	if updated.Name != "Renamed Lidarr" || !updated.Enabled {
		t.Errorf("updated = %+v, want the new name and enabled=true", updated)
	}

	opened, err := cache.DecryptedAPIKey(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedAPIKey: %v", err)
	}
	if opened.Reveal() != "stays-the-same" {
		t.Errorf("DecryptedAPIKey after UpdateArrInstance = %q, want the original key untouched", opened.Reveal())
	}
}

func TestListArrInstances_RejectsAnUnknownApp(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	_, err = cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: "not-a-real-app", Name: "Something", BaseURL: "http://example.invalid",
	}, secrets.Secret("key"), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateArrInstance succeeded with an unknown app")
	}
}

func TestDeleteArrInstance_RemovesIt(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppLidarr, Name: "Home Lidarr", BaseURL: "http://lidarr.local:8686",
	}, secrets.Secret("key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateArrInstance: %v", err)
	}

	if err := cache.DeleteArrInstance(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteArrInstance: %v", err)
	}
	instances, err := cache.ArrInstances(t.Context())
	if err != nil {
		t.Fatalf("ArrInstances: %v", err)
	}
	if len(instances) != 0 {
		t.Errorf("ArrInstances after delete = %+v, want none", instances)
	}
}
