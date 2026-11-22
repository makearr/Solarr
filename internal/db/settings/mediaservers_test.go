package settings_test

import (
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
