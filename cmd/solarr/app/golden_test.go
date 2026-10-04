package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvmaze"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/egress"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/logbuffer"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// updateGolden rewrites testdata/golden from what the server answers now: go test ./cmd/solarr/app -run Golden -update.
var updateGolden = flag.Bool("update", false, "rewrite the golden API answers")

// goldenSeededAt is when every seeded row was written, far enough back that "now" never collides with it.
var goldenSeededAt = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)

// goldenServer is the real server over real databases in a temporary directory, with nothing listening.
type goldenServer struct {
	process *process
	handler http.Handler
}

func newGoldenServer(t *testing.T) goldenServer {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()

	loaded := config.Default()
	loaded.Paths = config.Paths{
		ConfigDir: root, DataDir: filepath.Join(root, "data"), DBDir: filepath.Join(root, "data", "db"),
		CacheDir: filepath.Join(root, "data", "cache"), BackupDir: filepath.Join(root, "data", "backups"),
	}
	for _, dir := range []string{loaded.Paths.DBDir, loaded.Paths.CacheDir, loaded.Paths.BackupDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	key, err := config.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	loaded.Security.EncryptionKey = key
	loaded.Security.AuthMode = config.AuthNone
	loaded.Network.PublicIPCheck.Enabled = false

	ring := logbuffer.NewRing(slog.DiscardHandler, 16)
	running := &process{
		build: Build{Version: "golden"}, config: loaded, fsys: fsx.NewOS(), logger: slog.New(ring), logRing: ring,
		timezone: "UTC", configPath: filepath.Join(root, "config.yml"), tool: mediatool.New(nil, 0),
	}
	t.Cleanup(running.close)

	if err := running.openDatabases(ctx); err != nil {
		t.Fatalf("openDatabases: %v", err)
	}
	if err := running.ensureDefaultQualityProfile(ctx); err != nil {
		t.Fatalf("ensureDefaultQualityProfile: %v", err)
	}
	if err := running.startJobs(ctx); err != nil {
		t.Fatalf("startJobs: %v", err)
	}
	if err := running.seedAuthPolicy(ctx); err != nil {
		t.Fatalf("seedAuthPolicy: %v", err)
	}
	if err := running.ensureOpenAccessAdministrator(ctx); err != nil {
		t.Fatalf("ensureOpenAccessAdministrator: %v", err)
	}
	authConfig, err := running.authConfig()
	if err != nil {
		t.Fatalf("authConfig: %v", err)
	}
	server, err := running.newServer(authConfig, egress.New(egress.Config{Clock: clock.NewSystem(), Logger: running.logger}))
	if err != nil {
		t.Fatalf("newServer: %v", err)
	}
	return goldenServer{process: running, handler: server.Handler()}
}

// get answers one GET as the open-access administrator.
func (g goldenServer) get(t *testing.T, target string) []byte {
	t.Helper()
	recorder := httptest.NewRecorder()
	g.handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1"+target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, body = %s", target, recorder.Code, recorder.Body)
	}
	return recorder.Body.Bytes()
}

// goldenLibraries are the two seeded libraries and the ids the golden requests name.
type goldenLibraries struct {
	music, tv            int64
	album, artist        int64
	series, actor        int64
	network              int64
	musicRoot, seriesTop int64
}

func profileNamed(t *testing.T, snapshot *settings.Snapshot, name string) int64 {
	t.Helper()
	for _, profile := range snapshot.QualityProfiles() {
		if profile.Name == name {
			return profile.ID
		}
	}
	t.Fatalf("no quality profile named %q", name)
	return 0
}

func (g goldenServer) seedLibraries(t *testing.T) goldenLibraries {
	t.Helper()
	ctx := t.Context()
	cache := g.process.cache

	var out goldenLibraries
	for _, wanted := range []struct {
		mediaType, name, path string
		id, root              *int64
	}{
		{"music", "Music", "/library/music", &out.music, &out.musicRoot},
		{"tv", "Series", "/library/tv", &out.tv, &out.seriesTop},
	} {
		category, err := cache.CreateCategory(ctx, wanted.mediaType, wanted.name, 10)
		if err != nil {
			t.Fatalf("CreateCategory %s: %v", wanted.name, err)
		}
		rootFolder, err := cache.CreateRootFolder(ctx, wanted.path, wanted.name)
		if err != nil {
			t.Fatalf("CreateRootFolder %s: %v", wanted.path, err)
		}
		attachment := settings.CategoryRoot{CategoryID: category.ID, RootFolderID: rootFolder.ID, Weight: 1, AcceptsNew: true}
		if err := cache.AttachRootFolder(ctx, attachment); err != nil {
			t.Fatalf("AttachRootFolder %s: %v", wanted.path, err)
		}
		*wanted.id, *wanted.root = category.ID, rootFolder.ID
	}

	out.album, out.artist = g.seedAlbum(ctx, t, out)
	out.series = g.seedSeries(ctx, t, out)

	people := g.get(t, fmt.Sprintf("/series/people?category_id=%d&role=actor", out.tv))
	var cast struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(people, &cast); err != nil || len(cast.Items) == 0 {
		t.Fatalf("seeded series has no cast: %v, body = %s", err, people)
	}
	out.actor = cast.Items[0].ID

	var networks []struct {
		ID int64 `json:"id"`
	}
	listed := g.get(t, fmt.Sprintf("/series/networks?category_id=%d", out.tv))
	if err := json.Unmarshal(listed, &networks); err != nil || len(networks) == 0 {
		t.Fatalf("seeded series has no network: %v, body = %s", err, listed)
	}
	out.network = networks[0].ID
	return out
}

// seedAlbum writes one invented artist with one three-track album, two of its files on disk.
func (g goldenServer) seedAlbum(ctx context.Context, t *testing.T, libraries goldenLibraries) (albumID, artistID int64) {
	t.Helper()
	opened := g.process.library
	const releaseGroup, release = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"

	files, err := library.NewPhysical(opened).RegisterBatch(ctx, libraries.musicRoot, []library.PhysicalFile{
		{RelPath: "Amber Lanterns/Quiet Orchards (2019)/Amber Lanterns - 01 - First Light.flac", SizeBytes: 31_000_000, QuickHash: "golden-a1", FirstSeenAt: goldenSeededAt},
		{RelPath: "Amber Lanterns/Quiet Orchards (2019)/Amber Lanterns - 02 - Low Tide.flac", SizeBytes: 28_000_000, QuickHash: "golden-a2", FirstSeenAt: goldenSeededAt},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}

	identity := library.NewIdentity(opened)
	links := make([]library.WorkLink, 0, len(files))
	for _, file := range files {
		links = append(links, library.WorkLink{
			Provider: music.ProviderReleaseGroup, ExternalID: releaseGroup, MediaObjectID: file.MediaObjectID, Confidence: 1,
			Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "Quiet Orchards", SortTitle: "quiet orchards", PrimaryType: "Album"},
			Edition: &library.EditionLink{
				Provider: music.ProviderRelease, ExternalID: release, Confidence: 1,
				Edition: library.EditionIdentity{Kind: "release", Label: "Quiet Orchards"},
			},
			RootFolderID: libraries.musicRoot,
		})
	}
	resolved, err := identity.ResolveWorksDetailed(ctx, goldenSeededAt, links)
	if err != nil {
		t.Fatalf("ResolveWorksDetailed: %v", err)
	}
	albumID = resolved[0].WorkID
	if resolved[0].EditionID == nil {
		t.Fatal("the seeded album has no edition")
	}

	year, runtime := int64(2019), int64(200_000)
	parts := []library.PartIdentity{
		{MediaRef: "disc1:track1", SortKey: "d0001t0001", Title: "First Light", RuntimeMs: &runtime},
		{MediaRef: "disc1:track2", SortKey: "d0001t0002", Title: "Low Tide", RuntimeMs: &runtime},
		{MediaRef: "disc1:track3", SortKey: "d0001t0003", Title: "Harbour Bells", RuntimeMs: &runtime},
	}
	refinement := library.EditionRefinement{Kind: "release", Year: &year, Format: "CD"}
	if err := identity.RefineEdition(ctx, goldenSeededAt.Unix(), *resolved[0].EditionID, albumID, &year, "2019-05-17", "Album", refinement, parts); err != nil {
		t.Fatalf("RefineEdition: %v", err)
	}

	people := library.NewPeople(opened)
	artistID, err = people.ResolvePerson(ctx, goldenSeededAt, library.PersonLink{
		Provider: music.ProviderArtist, ExternalID: "33333333-3333-4333-8333-333333333333",
		Person: library.PersonIdentity{Name: "Amber Lanterns", Kind: "Group", Country: "IE", Born: "2011"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(ctx, albumID, artistID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	profile := profileNamed(t, g.process.cache.Snapshot(), shippedDefaultQualityProfileName)
	request := library.AlbumRequest{WorkID: albumID, EditionID: resolved[0].EditionID, CategoryID: libraries.music, ProfileID: profile, Monitored: true}
	if _, err := library.NewDesired(opened).EnsureAlbumItem(ctx, request); err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	return albumID, artistID
}

// goldenSkyhook answers one invented series: a finished first season and a second one still to air.
type goldenSkyhook struct{}

func (goldenSkyhook) Series(context.Context, int64) (skyhook.Series, error) {
	episode := func(id int64, season, number int, title, aired string) skyhook.Episode {
		return skyhook.Episode{
			TVDBShowID: 9001, TVDBID: id, SeasonNumber: season, EpisodeNumber: number, Title: title,
			AirDate: aired, AirDateUTC: aired + "T20:00:00Z", Runtime: 45, Overview: title + ", in short.",
		}
	}
	return skyhook.Series{
		TVDBID: 9001, Title: "The Marble Stations", Overview: "A ferry town keeps its own time.", Slug: "the-marble-stations",
		OriginalCountry: "gb", OriginalLanguage: "eng", Language: "eng", FirstAired: "2020-02-03", LastAired: "2020-02-17",
		Status: "Continuing", Runtime: 45, TimeOfDay: &skyhook.TimeOfDay{Hours: 21},
		OriginalNetwork: "Northern Channel", Network: "Northern Channel", Genres: []string{"Drama", "Mystery"},
		ContentRating: "TV-14", Rating: skyhook.Rating{Count: 120, Value: "7.9"},
		Actors:  []skyhook.Actor{{Name: "Iris Calder", Character: "The Harbourmaster"}, {Name: "Tom Aske", Character: "Finn"}},
		Seasons: []skyhook.Season{{SeasonNumber: 1}, {SeasonNumber: 2}},
		Episodes: []skyhook.Episode{
			episode(1, 1, 1, "Slack Water", "2020-02-03"), episode(2, 1, 2, "The Long Pier", "2020-02-10"),
			episode(3, 1, 3, "Signals", "2020-02-17"), episode(4, 2, 1, "Return Crossing", "2099-01-05"),
		},
	}, nil
}

func (goldenSkyhook) Search(context.Context, string) ([]skyhook.Series, error) { return nil, nil }

// goldenTVmaze knows no show, so the series comes from the one source alone.
type goldenTVmaze struct{}

func (goldenTVmaze) Show(context.Context, int64) (tvmaze.Show, error) {
	return tvmaze.Show{}, tvmaze.ErrNotFound
}
func (goldenTVmaze) LookupTVDB(context.Context, int64) (tvmaze.Show, error) {
	return tvmaze.Show{}, tvmaze.ErrNotFound
}
func (goldenTVmaze) Episodes(context.Context, int64) ([]tvmaze.Episode, error) {
	return nil, tvmaze.ErrNotFound
}
func (goldenTVmaze) Aliases(context.Context, int64) ([]string, error) { return nil, tvmaze.ErrNotFound }
func (goldenTVmaze) Cast(context.Context, int64) ([]tvmaze.CastMember, error) {
	return nil, tvmaze.ErrNotFound
}

// goldenTMDB adds what only TMDB knows: the cast, the creator, an episode's guest and director, and the network.
type goldenTMDB struct{}

func (goldenTMDB) Configured() bool { return true }

func (goldenTMDB) Series(context.Context, int64) (tmdb.Series, error) {
	series := tmdb.Series{
		ID: 7001, Name: "The Marble Stations", OriginalName: "The Marble Stations", Overview: "A ferry town keeps its own time.",
		FirstAirDate: "2020-02-03", LastAirDate: "2020-02-17", Status: "Returning Series", EpisodeRunTime: []int{45},
		OriginCountry: []string{"GB"}, OriginalLanguage: "en", VoteAverage: 7.9, VoteCount: 120,
		Networks:  []tmdb.Network{{ID: 501, Name: "Northern Channel", OriginCountry: "GB"}},
		CreatedBy: []tmdb.Creator{{ID: 303, Name: "Mara Voss"}},
		Seasons:   []tmdb.SeasonSummary{{SeasonNumber: 1, Name: "Season 1", AirDate: "2020-02-03", EpisodeCount: 3}},
	}
	series.ExternalIDs.TVDB = 9001
	series.AggregateCredits.Cast = []tmdb.CastMember{
		{ID: 301, Name: "Iris Calder", Order: 0, TotalEpisodeCount: 3, Roles: []tmdb.Role{{Character: "The Harbourmaster", EpisodeCount: 3}}},
		{ID: 302, Name: "Tom Aske", Order: 1, TotalEpisodeCount: 3, Roles: []tmdb.Role{{Character: "Finn", EpisodeCount: 3}}},
	}
	return series, nil
}

func (goldenTMDB) Season(context.Context, int64, int) (tmdb.Season, error) {
	return tmdb.Season{SeasonNumber: 1, Name: "Season 1", AirDate: "2020-02-03", Episodes: []tmdb.Episode{{
		ID: 801, SeasonNumber: 1, EpisodeNumber: 1, Name: "Slack Water", AirDate: "2020-02-03", Runtime: 45,
		GuestStars: []tmdb.GuestStar{{ID: 304, Name: "Odile Renn", Character: "The Pilot"}},
		Crew:       []tmdb.EpisodeCrew{{ID: 305, Name: "Hal Brandt", Job: "Director", Department: "Directing"}},
	}}}, nil
}

func (goldenTMDB) FindByTVDB(context.Context, int64) (tmdb.Hit, error) {
	return tmdb.Hit{ID: 7001}, nil
}
func (goldenTMDB) FindByIMDb(context.Context, string) (tmdb.Hit, error) {
	return tmdb.Hit{}, tmdb.ErrNotFound
}
func (goldenTMDB) Search(context.Context, string) ([]tmdb.Hit, error) { return nil, nil }

// seedSeries adds the invented series through the TV service, with the first two episodes on disk.
func (g goldenServer) seedSeries(ctx context.Context, t *testing.T, libraries goldenLibraries) int64 {
	t.Helper()
	opened := g.process.library
	series := library.NewSeries(opened)
	service := tv.Service{
		Skyhook: goldenSkyhook{}, TVmaze: goldenTVmaze{}, TMDB: goldenTMDB{}, Series: series, Reader: library.NewSeriesReader(opened),
		Desired: library.NewDesired(opened), Clock: clock.NewFake(goldenSeededAt),
	}
	added, err := service.Add(ctx, tv.AddRequest{
		IDs: tv.IDs{TVDB: 9001}, CategoryID: libraries.tv, Monitor: tv.MonitorAll,
		ProfileID: profileNamed(t, g.process.cache.Snapshot(), "Any"),
	})
	if err != nil {
		t.Fatalf("tv.Service.Add: %v", err)
	}

	files, err := library.NewPhysical(opened).RegisterBatch(ctx, libraries.seriesTop, []library.PhysicalFile{
		{RelPath: "The Marble Stations/Season 1/The Marble Stations - S01E01 - Slack Water.mkv", SizeBytes: 900_000_000, QuickHash: "golden-s1", FirstSeenAt: goldenSeededAt},
		{RelPath: "The Marble Stations/Season 1/The Marble Stations - S01E02 - The Long Pier.mkv", SizeBytes: 880_000_000, QuickHash: "golden-s2", FirstSeenAt: goldenSeededAt},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	links := []library.SeriesFileLink{
		{MediaObjectID: files[0].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 1)}}},
		{MediaObjectID: files[1].MediaObjectID, Parts: []library.PartKey{{MediaRef: tv.MediaRef(1, 2)}}},
	}
	attached, err := series.AttachFiles(ctx, goldenSeededAt, added.WorkID, libraries.seriesTop, links)
	if err != nil || attached.Attached != 2 {
		t.Fatalf("AttachFiles = %+v, %v, want two episodes attached", attached, err)
	}
	return added.WorkID
}

// withoutNow replaces every value that is the current time, so an answer is the same on any day.
func withoutNow(value any, now time.Time) any {
	const window = 48 * time.Hour
	switch typed := value.(type) {
	case map[string]any:
		for key, inner := range typed {
			typed[key] = withoutNow(inner, now)
		}
		return typed
	case []any:
		for index, inner := range typed {
			typed[index] = withoutNow(inner, now)
		}
		return typed
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, typed); err == nil && now.Sub(parsed).Abs() < window {
				return "<now>"
			}
		}
		return typed
	case float64:
		if seconds := time.Unix(int64(typed), 0); typed > 1e9 && now.Sub(seconds).Abs() < window {
			return "<now>"
		}
		return typed
	default:
		return value
	}
}

func goldenForm(t *testing.T, body []byte) []byte {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("answer is not JSON: %v\n%s", err, body)
	}
	formatted, err := json.MarshalIndent(withoutNow(decoded, time.Now()), "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	return append(formatted, '\n')
}

// TestGolden_ReadAnswersOfBothLibrariesStayTheSame pins what every list and detail endpoint answers for one
// seeded album and one seeded series, so the shared-core rewrite cannot change an answer without it showing.
func TestGolden_ReadAnswersOfBothLibrariesStayTheSame(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)

	requests := []struct{ name, target string }{
		{"mediatypes", "/mediatypes"},
		{"libraries", "/libraries"},
		{"music-artists", fmt.Sprintf("/libraries/%d/artists", ids.music)},
		{"music-albums", fmt.Sprintf("/libraries/%d/albums", ids.music)},
		{"music-tracks", fmt.Sprintf("/libraries/%d/tracks", ids.music)},
		{"music-works", fmt.Sprintf("/works?category_id=%d", ids.music)},
		{"music-works-wanted", fmt.Sprintf("/works?category_id=%d&wanted=true", ids.music)},
		{"music-work", fmt.Sprintf("/works/%d", ids.album)},
		{"music-people", "/people"},
		{"music-person", fmt.Sprintf("/people/%d", ids.artist)},
		{"tv-level-series", fmt.Sprintf("/libraries/%d/series", ids.tv)},
		{"tv-series", fmt.Sprintf("/series?category_id=%d", ids.tv)},
		{"tv-series-detail", fmt.Sprintf("/series/%d?category_id=%d", ids.series, ids.tv)},
		{"tv-wanted-missing", fmt.Sprintf("/series/wanted?category_id=%d", ids.tv)},
		{"tv-people", fmt.Sprintf("/series/people?category_id=%d", ids.tv)},
		{"tv-person", fmt.Sprintf("/series/people/%d?category_id=%d", ids.actor, ids.tv)},
		{"tv-networks", fmt.Sprintf("/series/networks?category_id=%d", ids.tv)},
		{"tv-network", fmt.Sprintf("/series/networks/%d?category_id=%d", ids.network, ids.tv)},
		{"calendar", "/calendar?from=2020-01-01&to=2020-12-31"},
	}

	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			got := goldenForm(t, server.get(t, request.target))
			path := filepath.Join("testdata", "golden", request.name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatalf("write %s: %v", path, err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden answer for %s (run with -update): %v", request.target, err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("GET %s changed.\n--- got\n%s\n--- want\n%s", request.target, got, want)
			}
		})
	}
}
