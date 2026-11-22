package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/secrets"
	"github.com/makearr/Solarr/migrations"
)

type servedBody func(*http.Request) string

func (f servedBody) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(f(request)))}, nil
}

func lidarrServing(body string) arrimport.LidarrClient {
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "arr-setup-test", UserAgent: "test", Transport: servedBody(func(*http.Request) string { return body }),
	}), "http://lidarr.local", "key")
}

// SYNC-7: an *Arr hides its keys, so an imported indexer with the mask is added switched off with no key; one reaching a
// Prowlarr connection here takes that connection's key.
func TestArrSetupAddsAMaskedIndexerSwitchedOffAndAProwlarrOneWithItsKey(t *testing.T) {
	cache := newSetupCache(t)
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableSecrets(cipher)
	if _, err := cache.CreateArrInstance(t.Context(), settings.ArrInstanceRequest{
		App: settings.ArrAppProwlarr, Name: "P", BaseURL: "http://prowlarr.local:9696", Enabled: true,
	}, secrets.Secret("prowlarr-key"), 1); err != nil {
		t.Fatal(err)
	}
	served := `[
	 {"name":"Direct","implementation":"Newznab","enableRss":true,"fields":[{"name":"baseUrl","value":"http://indexer.example"},{"name":"apiKey","value":"********"},{"name":"categories","value":[3010,3040]}]},
	 {"name":"ViaProwlarr","implementation":"Torznab","enableRss":true,"fields":[{"name":"baseUrl","value":"http://prowlarr.local:9696/4"},{"name":"apiKey","value":"********"}]}]`
	err = ArrSetup{Cache: cache, Clock: clock.NewFakeAtEpoch()}.indexers(t.Context(), settings.ArrAppLidarr, lidarrServing(served))
	if err == nil || !strings.Contains(err.Error(), "Direct") {
		t.Errorf("err = %v, want a note naming the indexer that needs a key", err)
	}
	indexers, _ := cache.Indexers(t.Context())
	byName := map[string]settings.Indexer{}
	for _, found := range indexers {
		byName[found.Name] = found
	}
	direct, via := byName["Direct"], byName["ViaProwlarr"]
	if direct.Enabled || direct.HasAPIKey {
		t.Errorf("masked indexer = %+v, want it off with no key", direct)
	}
	if !via.Enabled || !via.HasAPIKey {
		t.Errorf("Prowlarr indexer = %+v, want it on with Prowlarr's key", via)
	}
	if got := direct.StoredMusicCategories(); len(got) != 2 || got[0] != 3010 {
		t.Errorf("categories = %v, want the Lidarr ones carried", got)
	}
}

func TestMaskedIsAllAsterisks(t *testing.T) {
	for secret, want := range map[string]bool{"********": true, "*": true, "": false, "abc": false, "ab**": false} {
		if got := arrimport.Masked(secret); got != want {
			t.Errorf("Masked(%q) = %v, want %v", secret, got, want)
		}
	}
}

func newSetupCache(t *testing.T) *settings.Cache {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return cache
}

func newPushLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	return opened
}

// SYNC-2: a linked item Solarr holds no wanted item of is never written as unmonitored, and the value both sides last
// agreed on says which side moved.
func TestPusherSendsOnlyWhatSolarrChangedAndNeverForAnUnknownItem(t *testing.T) {
	opened := newPushLibrary(t)
	now := time.Unix(1_700_000_000, 0)
	ids, err := library.NewIdentity(opened).ResolveWorks(t.Context(), now, []library.WorkLink{
		{Provider: "musicbrainz:release-group", ExternalID: "a", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "A"}},
		{Provider: "musicbrainz:release-group", ExternalID: "b", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "B"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	desired, links := library.NewDesired(opened), library.NewArrLinks(opened)
	if _, err := desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{WorkID: ids[0], CategoryID: 1, ProfileID: 1, Monitored: false}); err != nil {
		t.Fatal(err)
	}
	// ids[1] has no wanted item at all.
	for _, link := range []library.ArrLink{
		{SourceKind: "album", SourceID: "1", LocalKind: "work", LocalID: ids[0]},
		{SourceKind: "album", SourceID: "2", LocalKind: "work", LocalID: ids[1]},
		{SourceKind: "album", SourceID: "3", LocalKind: "work", LocalID: ids[0]},
	} {
		if err := links.Link(t.Context(), 5, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := links.SetSynced(t.Context(), 5, library.ArrLink{SourceKind: "album", SourceID: "3"}, false); err != nil {
		t.Fatal(err)
	}
	stored, err := links.ForInstance(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]library.ArrLink{}
	for _, link := range stored {
		byID[link.SourceID] = link
	}
	var written []string
	write := func(_ context.Context, _ string, id int64, monitored bool) error {
		written = append(written, fmt.Sprintf("%d=%v", id, monitored))
		return nil
	}
	push := &pusher{sync: ArrSync{Desired: desired, Links: links}, instanceID: 5, links: map[string]map[string]library.ArrLink{"album": byID}}

	// 1: Solarr has the item unmonitored and the *Arr monitors it, never synced -> Solarr wins, written.
	// 2: no wanted item -> left alone and counted.
	// 3: Solarr unchanged since the last sync (false) while the *Arr turned it on -> left for the pull.
	for id, upstream := range map[int64]bool{1: true, 2: true, 3: true} {
		if err := push.send(t.Context(), "album", id, upstream, "/api/v1/album", write); err != nil {
			t.Fatal(err)
		}
	}
	if len(written) != 1 || written[0] != "1=false" {
		t.Errorf("written = %v, want only album 1 set to false", written)
	}
	if push.result.Unknown != 1 || push.result.Changed != 1 || push.result.Checked != 3 {
		t.Errorf("result = %+v, want 3 checked, 1 changed, 1 unknown", push.result)
	}
}

// SYNC-7: the remote path mapping of an imported download client's host comes along, so its finished downloads are found.
func TestArrSetupCarriesTheRemotePathMappingOfAClientsHost(t *testing.T) {
	cache := newSetupCache(t)
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	answers := map[string]string{
		"/api/v1/downloadclient": `[{"name":"Box","implementation":"Sabnzbd","protocol":"usenet","enable":true,"priority":3,
			"fields":[{"name":"host","value":"sab.example"},{"name":"port","value":8080}]},
			{"name":"Other","implementation":"Sabnzbd","protocol":"usenet","enable":true,"fields":[{"name":"host","value":"elsewhere.example"},{"name":"port","value":8081}]}]`,
		"/api/v1/remotepathmapping": `[{"id":1,"host":"SAB.example","remotePath":"/downloads/","localPath":"/data/usenet/"}]`,
	}
	client := arrimport.NewLidarrClient(httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "arr-setup-test", UserAgent: "test", Transport: servedBody(func(request *http.Request) string { return answers[request.URL.Path] }),
	}), "http://lidarr.local", "key")
	if err := (ArrSetup{Cache: cache, Clock: fake}).downloadClients(t.Context(), settings.ArrAppLidarr, client); err != nil {
		t.Fatalf("downloadClients: %v", err)
	}
	have, err := cache.Downloaders(t.Context())
	if err != nil || len(have) != 2 {
		t.Fatalf("downloaders = %v, %v", have, err)
	}
	options := map[string]settings.DownloaderOptions{}
	for _, entry := range have {
		var parsed settings.DownloaderOptions
		if entry.SettingsJSON != "" {
			if err := json.Unmarshal([]byte(entry.SettingsJSON), &parsed); err != nil {
				t.Fatal(err)
			}
		}
		options[entry.Name] = parsed
	}
	if got := options["Box"]; got.RemotePath != "/downloads/" || got.LocalPath != "/data/usenet/" || got.Priority == nil || *got.Priority != 3 {
		t.Errorf("Box options = %+v, want its host's mapping and priority 3", got)
	}
	if got := options["Other"]; got.RemotePath != "" || got.LocalPath != "" {
		t.Errorf("Other options = %+v, want no mapping: it is another host", got)
	}
}
