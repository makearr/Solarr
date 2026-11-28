package settings_test

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
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

// DB-17: library.db keeps links by connection id, so a deleted connection's id must not come back.
func TestDeleteArrInstance_ItsIDIsNeverGivenAgain(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableSecrets(cipher)
	create := func(name string) settings.ArrInstance {
		t.Helper()
		created, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
			App: settings.ArrAppLidarr, Name: name, BaseURL: "http://lidarr.local:8686",
		}, secrets.Secret("key"), fake.Now().Unix())
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	first := create("First")
	if err := cache.DeleteArrInstance(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	if second := create("Second"); second.ID == first.ID {
		t.Errorf("a new connection took the deleted id %d", first.ID)
	}
}

// SEC-14: a proxy's user and password are sealed apart from its URL, which the export and the screens see without them.
func TestSetNetworkSettings_SealsTheProxyCredentials(t *testing.T) {
	cache, fake := newCache(t)
	ctx := t.Context()
	wanted := settings.NetworkSettings{ProxyEnabled: true, ProxyURL: "socks5://kim:s3cret@proxy.local:1080", ProxyBypass: []string{}, AllowCIDRs: []string{}}
	if err := cache.SetNetworkSettings(ctx, fake, wanted, netip.Addr{}); err == nil {
		t.Fatal("credentials were accepted with no way to seal them")
	}
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableSecrets(cipher)
	if err := cache.SetNetworkSettings(ctx, fake, wanted, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	got := cache.Snapshot().NetworkSettings()
	if got.ProxyURL != "socks5://kim:s3cret@proxy.local:1080" || got.ProxyURLPublic != "socks5://proxy.local:1080" {
		t.Errorf("dialled %q, public %q", got.ProxyURL, got.ProxyURLPublic)
	}
	if exported := cache.Export().Settings.NetworkProxyURL; exported != "socks5://proxy.local:1080" {
		t.Errorf("exported %q, want no credentials", exported)
	}

	wanted.ProxyURL = "socks5://kim:xxxxx@proxy.local:1080"
	if err := cache.SetNetworkSettings(ctx, fake, wanted, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().NetworkSettings().ProxyURL; got != "socks5://kim:s3cret@proxy.local:1080" {
		t.Errorf("after sending the mask back: %q, want the password kept", got)
	}
	wanted.ProxyURL = "socks5://proxy.local:1080"
	if err := cache.SetNetworkSettings(ctx, fake, wanted, netip.Addr{}); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().NetworkSettings().ProxyURL; got != "socks5://proxy.local:1080" {
		t.Errorf("after clearing: %q", got)
	}
}

func TestArrPull_RootLibrariesSendAPathToTheLibraryOfTheLongestRootItLiesUnder(t *testing.T) {
	pull := settings.ArrPull{RootLibraries: []settings.ArrRootLibrary{{Root: "/tv", LibraryID: 1}, {Root: "/tv/anime/", LibraryID: 2}}}
	for path, want := range map[string]int64{"/tv/anime": 2, "/tv/anime/show": 2, "/tv/shows": 1, "/tv": 1} {
		if got, found := pull.LibraryOfRoot(path); !found || got != want {
			t.Errorf("LibraryOfRoot(%q) = %d, %v; want %d", path, got, found, want)
		}
	}
	if _, found := pull.LibraryOfRoot("/tvshows"); found {
		t.Error("/tvshows is not under /tv")
	}
	if root, found := pull.RootOfLibrary(2); !found || root != "/tv/anime/" {
		t.Errorf("RootOfLibrary(2) = %q, %v", root, found)
	}
	if _, found := pull.RootOfLibrary(9); found {
		t.Error("a library with no root chosen has none")
	}
}
