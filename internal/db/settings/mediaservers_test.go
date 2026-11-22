package settings_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func TestMediaServer_RoundTripsWithASealedCredentialAndMappings(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	ctx := t.Context()

	created, err := cache.CreateMediaServer(ctx, settings.MediaServerRequest{
		Kind: "jellyfin", Name: "Living room", BaseURL: "http://media.local:8096", Enabled: true,
		MediaServerUse: settings.MediaServerUse{PushFiles: true},
	}, secrets.Secret("key-123"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateMediaServer: %v", err)
	}
	want := settings.MediaServer{
		ID: created.ID, Kind: "jellyfin", Name: "Living room", BaseURL: "http://media.local:8096", Enabled: true,
		CreatedAt: fake.Now().Unix(), UpdatedAt: fake.Now().Unix(), MediaServerUse: settings.MediaServerUse{PushFiles: true},
	}
	if !reflect.DeepEqual(created, want) {
		t.Errorf("created = %+v, want %+v", created, want)
	}
	opened, err := cache.DecryptedMediaServerCredential(ctx, created.ID)
	if err != nil || opened.Reveal() != "key-123" {
		t.Errorf("credential = %q, %v; want key-123", opened.Reveal(), err)
	}

	paths := []settings.MediaServerPath{{SolarrPrefix: "/music", ServerPrefix: "/data/music"}}
	if err := cache.SetMediaServerPaths(ctx, created.ID, paths); err != nil {
		t.Fatalf("SetMediaServerPaths: %v", err)
	}
	gotPaths, err := cache.MediaServerPaths(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(gotPaths, paths) {
		t.Errorf("paths = %+v, %v; want %+v", gotPaths, err, paths)
	}
	users := []settings.MediaServerUser{{ServerUserID: "u1", Name: "guest", Counted: true}}
	if err := cache.SetMediaServerUsers(ctx, created.ID, users); err != nil {
		t.Fatalf("SetMediaServerUsers: %v", err)
	}
	gotUsers, err := cache.MediaServerUsers(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(gotUsers, users) {
		t.Errorf("users = %+v, %v; want %+v", gotUsers, err, users)
	}

	if err := cache.DeleteMediaServer(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMediaServer: %v", err)
	}
	if gotPaths, _ = cache.MediaServerPaths(ctx, created.ID); len(gotPaths) != 0 {
		t.Errorf("paths after delete = %+v, want none", gotPaths)
	}
}

func TestCreateMediaServer_RefusesAnEmptyCredential(t *testing.T) {
	cache, fake := newCache(t)
	cipher, _ := secrets.New(testCipherKey())
	cache.EnableSecrets(cipher)
	_, err := cache.CreateMediaServer(t.Context(), settings.MediaServerRequest{Kind: "jellyfin", Name: "n", BaseURL: "http://x"}, secrets.Secret(""), fake.Now().Unix())
	if err == nil {
		t.Fatal("CreateMediaServer accepted an empty credential")
	}
}

func TestSetMediaServerLibraries_OffersTheLocalProviderToTheLibraryAtOnce(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	ctx := t.Context()
	library, err := cache.CreateCategory(ctx, "tv", "Series", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	created, err := cache.CreateMediaServer(ctx, settings.MediaServerRequest{
		Kind: "jellyfin", Name: "Den", BaseURL: "http://media.local:8096", Enabled: true,
		MediaServerUse: settings.MediaServerUse{AsMetadata: true},
	}, secrets.Secret("key-123"), fake.Now().Unix())
	if err != nil {
		t.Fatalf("CreateMediaServer: %v", err)
	}
	mapping := []settings.MediaServerLibrary{{CategoryID: library.ID, ServerLibraryID: "lib-1"}}
	if err := cache.SetMediaServerLibraries(ctx, created.ID, mapping); err != nil {
		t.Fatalf("SetMediaServerLibraries: %v", err)
	}
	if !cache.Snapshot().LocalSourceOffered(library.ID, settings.LocalSource("jellyfin")) {
		t.Error("the local provider is not offered to the mapped library")
	}
	if err := cache.DeleteMediaServer(ctx, created.ID); err != nil {
		t.Fatalf("DeleteMediaServer: %v", err)
	}
	if cache.Snapshot().LocalSourceOffered(library.ID, settings.LocalSource("jellyfin")) {
		t.Error("the local provider is still offered after the connection is deleted")
	}
}

func TestDeleteMediaServer_RefusedWhileALibraryOrderNamesItUnlessAnotherOfItsKindAnswers(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	cache.EnableSecrets(cipher)
	ctx := t.Context()
	library, err := cache.CreateCategory(ctx, "tv", "Series", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	add := func(name string) settings.MediaServer {
		server, err := cache.CreateMediaServer(ctx, settings.MediaServerRequest{
			Kind: "jellyfin", Name: name, BaseURL: "http://media.local:8096", Enabled: true,
			MediaServerUse: settings.MediaServerUse{AsMetadata: true},
		}, secrets.Secret("key"), fake.Now().Unix())
		if err != nil {
			t.Fatalf("CreateMediaServer: %v", err)
		}
		if err := cache.SetMediaServerLibraries(ctx, server.ID, []settings.MediaServerLibrary{{CategoryID: library.ID, ServerLibraryID: "lib"}}); err != nil {
			t.Fatalf("SetMediaServerLibraries: %v", err)
		}
		return server
	}
	first, second := add("Den"), add("Attic")
	if err := cache.SetCategoryMetadataSources(ctx, library.ID, []string{"wikidata", settings.LocalSource("jellyfin")}); err != nil {
		t.Fatalf("SetCategoryMetadataSources: %v", err)
	}
	if err := cache.DeleteMediaServer(ctx, first.ID); err != nil {
		t.Errorf("deleting one of two connections of a kind = %v, want nil", err)
	}
	if err := cache.DeleteMediaServer(ctx, second.ID); !errors.Is(err, settings.ErrInUse) {
		t.Errorf("deleting the last connection a library's order names = %v, want ErrInUse", err)
	}
	if err := cache.SetCategoryMetadataSources(ctx, library.ID, nil); err != nil {
		t.Fatalf("SetCategoryMetadataSources: %v", err)
	}
	if err := cache.DeleteMediaServer(ctx, second.ID); err != nil {
		t.Errorf("deleting once the order no longer names it = %v, want nil", err)
	}
}
