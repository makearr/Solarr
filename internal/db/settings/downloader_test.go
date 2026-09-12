package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/platform/secrets"
)

func TestCreateDownloader_RoundTripsThroughSealAndOpen(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}
	if created.Name != "SABnzbd" || created.Impl != "sabnzbd" || !created.HasCredentials {
		t.Errorf("created = %+v, want name=SABnzbd impl=sabnzbd HasCredentials=true", created)
	}

	opened, err := cache.DecryptedDownloaderCredentials(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedDownloaderCredentials: %v", err)
	}
	if opened.Reveal() != "a-real-api-key" {
		t.Errorf("DecryptedDownloaderCredentials = %q, want the original key back", opened.Reveal())
	}
}

func TestCreateDownloader_RefusesARealCredentialWithoutSecretsEnabled(t *testing.T) {
	cache, fake := newCache(t)
	// EnableSecrets deliberately not called.

	_, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("a-real-api-key"), fake.Now().Unix())
	if !errors.Is(err, settings.ErrSecretsUnavailable) {
		t.Errorf("err = %v, want ErrSecretsUnavailable", err)
	}
}

func TestCreateDownloader_RefusesAnUnknownProtocol(t *testing.T) {
	cache, fake := newCache(t)

	_, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "ftp",
	}, secrets.Secret(""), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateDownloader succeeded with an unknown protocol")
	}
}

func TestCreateDownloader_RefusesAnEmptyName(t *testing.T) {
	cache, fake := newCache(t)

	_, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "  ", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret(""), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateDownloader succeeded with a blank name")
	}
}

func TestSetDownloaderCredentials_ReplacesTheSealedValue(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("old-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	if err := cache.SetDownloaderCredentials(t.Context(), created.ID, secrets.Secret("new-key"), fake.Now().Unix()); err != nil {
		t.Fatalf("SetDownloaderCredentials: %v", err)
	}
	opened, err := cache.DecryptedDownloaderCredentials(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedDownloaderCredentials: %v", err)
	}
	if opened.Reveal() != "new-key" {
		t.Errorf("DecryptedDownloaderCredentials = %q, want %q", opened.Reveal(), "new-key")
	}
}

func TestUpdateDownloader_ChangesFactsNeverTheCredential(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)

	created, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret("the-key"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	updated, err := cache.UpdateDownloader(t.Context(), created.ID, settings.DownloaderRequest{
		Name: "SABnzbd Renamed", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: false, Protocol: "usenet",
	}, fake.Now().Unix())
	if err != nil {
		t.Fatalf("UpdateDownloader: %v", err)
	}
	if updated.Name != "SABnzbd Renamed" || updated.Enabled {
		t.Errorf("updated = %+v, want the new name/enabled", updated)
	}

	opened, err := cache.DecryptedDownloaderCredentials(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("DecryptedDownloaderCredentials: %v", err)
	}
	if opened.Reveal() != "the-key" {
		t.Errorf("DecryptedDownloaderCredentials = %q, want the original untouched by UpdateDownloader", opened.Reveal())
	}
}

func TestDeleteDownloader_RemovesIt(t *testing.T) {
	cache, fake := newCache(t)

	created, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}

	if err := cache.DeleteDownloader(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteDownloader: %v", err)
	}
	if _, err := cache.GetDownloader(t.Context(), created.ID); err == nil {
		t.Error("GetDownloader succeeded after delete, want an error")
	}
}
