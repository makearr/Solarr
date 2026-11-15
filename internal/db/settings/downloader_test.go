package settings_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
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

// openUsage is a library.db stand-in: a fixed number of unfinished downloads and held releases.
type openUsage struct{ open, held int64 }

func (u openUsage) OpenOnDownloader(context.Context, int64) (int64, error) { return u.open, nil }
func (u openUsage) OpenFromIndexer(context.Context, int64) (int64, int64, error) {
	return u.open, u.held, nil
}

func createUsedDownloader(t *testing.T, usage openUsage) (*settings.Cache, settings.Downloader, int64) {
	t.Helper()
	cache, fake := newCache(t)
	created, err := cache.CreateDownloader(t.Context(), settings.DownloaderRequest{
		Name: "SABnzbd", Impl: "sabnzbd", BaseURL: "http://10.0.1.134:8080", Enabled: true, Protocol: "usenet",
	}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateDownloader: %v", err)
	}
	cache.EnableDownloadUsage(usage)
	return cache, created, fake.Now().Unix()
}

func TestDeleteDownloader_RefusedWhileDownloadsRun(t *testing.T) {
	cache, created, _ := createUsedDownloader(t, openUsage{open: 3})

	err := cache.DeleteDownloader(t.Context(), created.ID)
	var inUse *settings.InUseError
	if !errors.As(err, &inUse) || !errors.Is(err, settings.ErrInUse) {
		t.Fatalf("DeleteDownloader = %v, want an InUseError", err)
	}
	if want := []settings.Reference{{Kind: "running downloads", Count: 3}}; !slices.Equal(inUse.References, want) {
		t.Errorf("References = %+v, want %+v", inUse.References, want)
	}
	if _, err := cache.GetDownloader(t.Context(), created.ID); err != nil {
		t.Errorf("the refused client is gone: %v", err)
	}
}

func TestUpdateDownloader_ChangingKindOrAddressUnderRunningDownloads(t *testing.T) {
	cache, created, at := createUsedDownloader(t, openUsage{open: 2})
	request := func(edit func(*settings.DownloaderRequest)) settings.DownloaderRequest {
		r := settings.DownloaderRequest{Name: created.Name, Impl: created.Impl, BaseURL: created.BaseURL, Enabled: true, Protocol: created.Protocol}
		edit(&r)
		return r
	}

	if _, err := cache.UpdateDownloader(t.Context(), created.ID, request(func(r *settings.DownloaderRequest) { r.Impl = "nzbget" }), at); !errors.Is(err, settings.ErrInUse) {
		t.Errorf("kind change = %v, want ErrInUse", err)
	}
	if _, err := cache.UpdateDownloader(t.Context(), created.ID, request(func(r *settings.DownloaderRequest) { r.BaseURL = "http://10.0.1.135:8080" }), at); !errors.Is(err, settings.ErrMovedUnconfirmed) {
		t.Errorf("unconfirmed move = %v, want ErrMovedUnconfirmed", err)
	}
	moved, err := cache.UpdateDownloader(t.Context(), created.ID, request(func(r *settings.DownloaderRequest) {
		r.BaseURL = "http://10.0.1.135:8080"
		r.ConfirmMoved = true
	}), at)
	if err != nil || moved.BaseURL != "http://10.0.1.135:8080" {
		t.Errorf("confirmed move = %+v, %v, want the new address", moved, err)
	}
	if _, err := cache.UpdateDownloader(t.Context(), created.ID, request(func(r *settings.DownloaderRequest) { r.Name = "Renamed"; r.BaseURL = moved.BaseURL }), at); err != nil {
		t.Errorf("a rename under running downloads = %v, want it allowed", err)
	}
}

func TestDeleteIndexer_RefusedWhileDownloadsOrHeldReleasesCameFromIt(t *testing.T) {
	cache, fake := newCache(t)
	created, err := cache.CreateIndexer(t.Context(), settings.IndexerRequest{Name: "Usenet one", Impl: "newznab", BaseURL: "http://idx.example", Enabled: true}, secrets.Secret(""), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateIndexer: %v", err)
	}
	cache.EnableDownloadUsage(openUsage{held: 1})

	err = cache.DeleteIndexer(t.Context(), created.ID)
	var inUse *settings.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteIndexer = %v, want an InUseError", err)
	}
	if want := []settings.Reference{{Kind: "held releases", Count: 1}}; !slices.Equal(inUse.References, want) {
		t.Errorf("References = %+v, want %+v", inUse.References, want)
	}

	cache.EnableDownloadUsage(openUsage{})
	if err := cache.DeleteIndexer(t.Context(), created.ID); err != nil {
		t.Errorf("DeleteIndexer with nothing in use = %v", err)
	}
}
