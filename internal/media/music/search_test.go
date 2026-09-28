package music_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

// searchTestEnv is a real (temp file) library.db and settings.db together --
// the candidate pipeline needs both, unlike most of this package's other
// tests.
type searchTestEnv struct {
	library      *store.Store
	settings     *settings.Cache
	clock        *clock.Fake
	works        library.Works
	desired      library.Desired
	candidates   library.Candidates
	acquisitions library.Acquisitions
}

func newSearchTestEnv(t *testing.T) *searchTestEnv {
	t.Helper()
	fake := clock.NewFakeAtEpoch()

	libDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(libDir, "backups"), 0o755); err != nil {
		t.Fatalf("mkdir library backups: %v", err)
	}
	libraryStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(libDir, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	t.Cleanup(func() { _ = libraryStore.Close() })
	libraryOwner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations.Library: %v", err)
	}
	if err := store.Migrate(t.Context(), libraryStore, filepath.Join(libDir, "backups"), libraryOwner); err != nil {
		t.Fatalf("migrate library: %v", err)
	}

	settingsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(settingsDir, "backups"), 0o755); err != nil {
		t.Fatalf("mkdir settings backups: %v", err)
	}
	settingsStore, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(settingsDir, "settings.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	t.Cleanup(func() { _ = settingsStore.Close() })
	settingsOwner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("migrations.Settings: %v", err)
	}
	if err := store.Migrate(t.Context(), settingsStore, filepath.Join(settingsDir, "backups"), settingsOwner); err != nil {
		t.Fatalf("migrate settings: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), settingsStore)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	return &searchTestEnv{
		library: libraryStore, settings: cache, clock: fake,
		works: library.NewWorks(libraryStore), desired: library.NewDesired(libraryStore),
		candidates: library.NewCandidates(libraryStore), acquisitions: library.NewAcquisitions(libraryStore),
	}
}

// searchFn builds the Search under test, over env's own stores.
func (env *searchTestEnv) search() music.Search {
	return music.Search{
		Works: env.works, Desired: env.desired, Candidates: env.candidates,
		Settings: env.settings, Clock: env.clock,
	}
}

// createWork inserts a work row directly (CreateWork's own params, not the
// find-or-create Identity path) -- the fastest way to a work with a
// specific year, which WorkIdentity has no field for.
func (env *searchTestEnv) createWork(t *testing.T, title string, year int64) int64 {
	t.Helper()
	var created librarydb.Work
	err := env.library.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		row, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: "music", CanonicalTitle: title, SortTitle: title,
			Year:    sql.NullInt64{Int64: year, Valid: year > 0},
			AddedAt: env.clock.Now().Unix(), UpdatedAt: env.clock.Now().Unix(),
		})
		if err != nil {
			return err
		}
		created = row
		// Only an album with a known tracklist is searched.
		_, err = queries.CreateWorkPart(ctx, librarydb.CreateWorkPartParams{
			WorkID: row.ID, MediaRef: "disc1:track1", SortKey: "0001", Monitored: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("CreateWork: %v", err)
	}
	return created.ID
}

// createSearchableWork stands up a work, its artist credit and an
// album-level (SET) desired item against a quality profile with rulesJSON
// -- everything SearchItem needs to run against it.
func (env *searchTestEnv) createSearchableWork(t *testing.T, artist, title string, year int64, rulesJSON string) librarydb.DesiredItem {
	t.Helper()
	people := library.NewPeople(env.library)
	workID := env.createWork(t, title, year)

	personID, err := people.ResolvePerson(t.Context(), env.clock.Now(), library.PersonLink{
		Provider: "test", ExternalID: artist,
		Person: library.PersonIdentity{Name: artist, SortName: artist},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), workID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}

	profile, err := env.settings.CreateQualityProfile(t.Context(), title+"-profile", rulesJSON,
		`{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := env.settings.CreateCategory(t.Context(), "music", title+"-category", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	item, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profile.ID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	return item
}

const alwaysAcceptRules = `{"quality_profile":"always-accept","compare":["score"],"tiers":{},"targets":{}}`
const rejectMP3Rules = `{"quality_profile":"reject-mp3","require":{"reject_codecs":["mp3"]},"compare":["score"],"tiers":{},"targets":{}}`

// fakeIndexerSearcher answers canned RawItems per call, recording every
// request it saw so a test can assert exactly which tiers actually searched.
type fakeIndexerSearcher struct {
	responses func(request indexer.SearchRequest) ([]indexer.RawItem, error)
	requests  []indexer.SearchRequest
}

func (f *fakeIndexerSearcher) Search(_ context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error) {
	f.requests = append(f.requests, request)
	if f.responses == nil {
		return nil, nil
	}
	return f.responses(request)
}

func rawItem(guid, title string) indexer.RawItem {
	return indexer.RawItem{
		Title: title, GUID: guid, Link: "https://indexer.example/nzb/" + guid,
		SizeBytes: 500_000_000, PublishedAt: time.Unix(1_700_000_000, 0),
	}
}

func TestSearchItem_ExactTierAcceptsAMatchingRelease(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)

	searcher := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{rawItem("guid-1", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
		},
	}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if result.TierUsed == nil || *result.TierUsed != 0 {
		t.Fatalf("TierUsed = %v, want tier 0 (exact)", result.TierUsed)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("%d decisions, want 1", len(result.Decisions))
	}
	if result.Decisions[0].Decision.Outcome != "ACCEPT" {
		t.Errorf("Outcome = %q, want ACCEPT for a profile with no gates", result.Decisions[0].Decision.Outcome)
	}
	if result.Decisions[0].ReleaseCandidate.Protocol != "usenet" {
		t.Errorf("Protocol = %q, want usenet for a newznab connection", result.Decisions[0].ReleaseCandidate.Protocol)
	}
	if len(searcher.requests) != 2 {
		t.Errorf("%d requests, want tier 0's structured and text query only", len(searcher.requests))
	}

	// The decision is queryable back through the repository, not just the
	// in-memory result -- section 12's own "why wasn't this downloaded" query.
	stored, err := env.candidates.ListDecisionsForItem(t.Context(), item.ID, 10)
	if err != nil {
		t.Fatalf("ListDecisionsForItem: %v", err)
	}
	if len(stored) != 1 || stored[0].ReleaseCandidate.GUID != "guid-1" {
		t.Errorf("stored = %+v, want the one persisted candidate", stored)
	}
}

func TestSearchItem_WidensToTheYearTierWhenExactFindsNothing(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)

	calls := 0
	searcher := &fakeIndexerSearcher{
		responses: func(request indexer.SearchRequest) ([]indexer.RawItem, error) {
			calls++
			if request.Extra["year"] == "" {
				return nil, nil // tier 0: nothing
			}
			return []indexer.RawItem{rawItem("guid-2", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
		},
	}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if result.TierUsed == nil || *result.TierUsed != 1 {
		t.Fatalf("TierUsed = %v, want tier 1 (+year) once tier 0 found nothing", result.TierUsed)
	}
	if calls != 4 {
		t.Errorf("indexer called %d times, want 4 (structured and text, tier 0 then tier 1)", calls)
	}
}

func TestSearchItem_UnrelatedReleasesAreNeverPersisted(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)

	searcher := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{rawItem("guid-3", "Wrong Artist-Totally Different Album-1999-FLAC-GROUP")}, nil
		},
	}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if result.TierUsed != nil {
		t.Errorf("TierUsed = %v, want nil -- nothing here should have cleared the match-distance gate", *result.TierUsed)
	}
	if len(result.Decisions) != 0 {
		t.Errorf("%d decisions, want 0", len(result.Decisions))
	}

	stored, err := env.candidates.ListDecisionsForItem(t.Context(), item.ID, 10)
	if err != nil {
		t.Fatalf("ListDecisionsForItem: %v", err)
	}
	if len(stored) != 0 {
		t.Errorf("stored = %+v, an unrelated release must never be persisted as a candidate for this item", stored)
	}
}

func TestSearchItem_RejectedByQualityGateIsStillPersisted(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, rejectMP3Rules)

	searcher := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{rawItem("guid-4", "Nosi Zada-Discovery-2001-MP3-320-GROUP")}, nil
		},
	}

	result, err := env.search().SearchItem(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("%d decisions, want 1 -- a rejection is a recorded finding, not silence", len(result.Decisions))
	}
	if result.Decisions[0].Decision.Outcome != "REJECT" {
		t.Errorf("Outcome = %q, want REJECT for an mp3 against a reject_codecs:[mp3] profile", result.Decisions[0].Decision.Outcome)
	}
}

func TestSearchItem_OneIndexerFailingDoesNotStopAnother(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)

	failing := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return nil, errors.New("connection refused")
		},
	}
	working := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{rawItem("guid-5", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
		},
	}

	result, err := env.search().SearchItem(t.Context(), item.ID, []music.IndexerConn{
		{ID: 1, Impl: "newznab", Client: failing},
		{ID: 2, Impl: "newznab", Client: working},
	}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if len(result.IndexerErrors) != 1 || result.IndexerErrors[0].IndexerID != 1 {
		t.Errorf("IndexerErrors = %+v, want indexer 1's own failure recorded", result.IndexerErrors)
	}
	if len(result.Decisions) != 1 {
		t.Fatalf("%d decisions, want 1 from the indexer that did answer", len(result.Decisions))
	}
}

func TestSearchItem_TrackLevelItemsAreNotSupportedYet(t *testing.T) {
	env := newSearchTestEnv(t)
	workID := env.createWork(t, "Discovery", 2001)

	profile, err := env.settings.CreateQualityProfile(t.Context(), "p", alwaysAcceptRules, "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := env.settings.CreateCategory(t.Context(), "music", "c", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// A SINGLE (track-level) desired item, created directly rather than
	// through EnsureTrackItem (which needs a real work_part to resolve
	// against) -- this test only cares about SearchItem's own guard against
	// the satisfaction kind, not track-item creation itself.
	var item librarydb.DesiredItem
	err = env.library.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).CreateDesiredItem(ctx, librarydb.CreateDesiredItemParams{
			WorkID: workID, MediaRef: "disc1:track1",
			CategoryID: category.ID, ProfileID: profile.ID, Monitored: 1,
			SatisfactionKind: "SINGLE", RequiredCount: sql.NullInt64{Int64: 1, Valid: true},
			State: "MISSING", Dirty: 1,
		})
		item = row
		return err
	})
	if err != nil {
		t.Fatalf("CreateDesiredItem: %v", err)
	}

	_, err = env.search().SearchItem(t.Context(), item.ID, nil, env.clock.Now())
	if !errors.Is(err, music.ErrTrackSearchNotSupported) {
		t.Errorf("err = %v, want ErrTrackSearchNotSupported", err)
	}
}

func TestSearchCategoryJob_SearchesEveryIncompleteItemInTheCategory(t *testing.T) {
	env := newSearchTestEnv(t)

	// Two albums, deliberately created via createSearchableWork (which
	// gives each its own quality profile and category) is the wrong shape
	// here -- SearchCategoryJob needs both items in the *same* category, so
	// this test builds that directly instead.
	profile, err := env.settings.CreateQualityProfile(t.Context(), "shared-profile", alwaysAcceptRules,
		`{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := env.settings.CreateCategory(t.Context(), "music", "shared-category", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	people := library.NewPeople(env.library)
	personID, err := people.ResolvePerson(t.Context(), env.clock.Now(), library.PersonLink{
		Provider: "test", ExternalID: "Nosi Zada", Person: library.PersonIdentity{Name: "Nosi Zada", SortName: "Nosi Zada"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}

	discoveryID := env.createWork(t, "Discovery", 2001)
	if err := people.AddCredit(t.Context(), discoveryID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit (Discovery): %v", err)
	}
	homeworkID := env.createWork(t, "Homework", 1997)
	if err := people.AddCredit(t.Context(), homeworkID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit (Homework): %v", err)
	}
	discoveryItem, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: discoveryID, CategoryID: category.ID, ProfileID: profile.ID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem (Discovery): %v", err)
	}
	homeworkItem, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: homeworkID, CategoryID: category.ID, ProfileID: profile.ID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem (Homework): %v", err)
	}

	searcher := &fakeIndexerSearcher{
		responses: func(request indexer.SearchRequest) ([]indexer.RawItem, error) {
			switch request.Extra["album"] {
			case "Discovery":
				return []indexer.RawItem{rawItem("guid-discovery", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
			case "Homework":
				return []indexer.RawItem{rawItem("guid-homework", "Nosi Zada-Homework-1997-FLAC-GROUP")}, nil
			default:
				return nil, nil
			}
		},
	}
	sender := &fakeSabnzbdSender{clientRef: "nzo"}
	job := music.NewSearchCategoryJob(music.SearchCategoryConfig{
		Grab: music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
			{ID: 5, Protocol: "usenet", Client: sender},
		})},
		Indexers: func(context.Context) ([]music.IndexerConn, error) {
			return []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, nil
		},
		Clock: env.clock, Logger: slog.New(slog.DiscardHandler),
	})

	payload, err := json.Marshal(music.SearchCategoryPayload{CategoryID: category.ID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := job.Run(t.Context(), jobs.Job{Payload: payload}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// "Search all" is Lidarr's automatic search: every album's best release is sent.
	var sent []string
	for _, request := range sender.requests {
		sent = append(sent, request.Name)
	}
	slices.Sort(sent)
	if want := []string{"Nosi Zada-Discovery-2001-FLAC-GROUP", "Nosi Zada-Homework-1997-FLAC-GROUP"}; !slices.Equal(sent, want) {
		t.Errorf("sent %v, want %v", sent, want)
	}

	for _, item := range []struct {
		name string
		id   int64
	}{{"Discovery", discoveryItem.ID}, {"Homework", homeworkItem.ID}} {
		decisions, err := env.candidates.ListDecisionsForItem(t.Context(), item.id, 10)
		if err != nil {
			t.Fatalf("ListDecisionsForItem(%s): %v", item.name, err)
		}
		if len(decisions) != 1 {
			t.Errorf("%s: %d decisions, want 1", item.name, len(decisions))
		}
	}
}

func TestSearchCategoryJob_OneItemFailingDoesNotStopTheRest(t *testing.T) {
	env := newSearchTestEnv(t)
	category, err := env.settings.CreateCategory(t.Context(), "music", "cat", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	profile, err := env.settings.CreateQualityProfile(t.Context(), "p", alwaysAcceptRules, "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	// A desired item whose profile_id resolves to nothing (a deleted
	// profile, or any other stale reference -- library.db carries no FK
	// into settings.db, ADR-25) fails ErrNoQualityProfile. SearchCategoryJob
	// must still search its sibling in the same category.
	brokenWorkID := env.createWork(t, "Broken", 2000)
	if _, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: brokenWorkID, CategoryID: category.ID, ProfileID: 999_999, Monitored: true,
	}); err != nil {
		t.Fatalf("EnsureAlbumItem (broken): %v", err)
	}

	people := library.NewPeople(env.library)
	workingWorkID := env.createWork(t, "Discovery", 2001)
	personID, err := people.ResolvePerson(t.Context(), env.clock.Now(), library.PersonLink{
		Provider: "test", ExternalID: "Nosi Zada", Person: library.PersonIdentity{Name: "Nosi Zada", SortName: "Nosi Zada"},
	})
	if err != nil {
		t.Fatalf("ResolvePerson: %v", err)
	}
	if err := people.AddCredit(t.Context(), workingWorkID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	workingItem, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: workingWorkID, CategoryID: category.ID, ProfileID: profile.ID, Monitored: true,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem (working): %v", err)
	}

	searcher := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{rawItem("guid-x", "Nosi Zada-Discovery-2001-FLAC-GROUP")}, nil
		},
	}
	job := music.NewSearchCategoryJob(music.SearchCategoryConfig{
		Grab: music.Grab{Search: env.search(), Dispatch: env.dispatch([]music.DownloaderConn{
			{ID: 5, Protocol: "usenet", Client: &fakeSabnzbdSender{clientRef: "nzo"}},
		})},
		Indexers: func(context.Context) ([]music.IndexerConn, error) {
			return []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, nil
		},
		Clock: env.clock, Logger: slog.New(slog.DiscardHandler),
	})

	payload, err := json.Marshal(music.SearchCategoryPayload{CategoryID: category.ID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := job.Run(t.Context(), jobs.Job{Payload: payload}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	decisions, err := env.candidates.ListDecisionsForItem(t.Context(), workingItem.ID, 10)
	if err != nil {
		t.Fatalf("ListDecisionsForItem: %v", err)
	}
	if len(decisions) != 1 {
		t.Errorf("%d decisions for the working item, want 1 despite the sibling's own failure", len(decisions))
	}
}

// Blocklisting a decided release keeps it out of every later search, before it is decided again.
func TestSearchItem_ABlocklistedReleaseIsNeverDecidedAgain(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{
			rawItem("guid-1", "Nosi Zada-Discovery-2001-FLAC-GROUP"),
			rawItem("guid-2", "Nosi Zada-Discovery-2001-MP3-OTHER"),
		}, nil
	}}
	blocklist := library.NewBlocklistStore(env.library)
	search := env.search()
	search.Blocklist = &blocklist
	conns := []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}

	first, err := search.SearchItem(t.Context(), item.ID, conns, env.clock.Now())
	if err != nil || len(first.Decisions) != 2 {
		t.Fatalf("first search: %d decisions, %v; want 2", len(first.Decisions), err)
	}
	var blocked int64
	for _, detail := range first.Decisions {
		if detail.ReleaseCandidate.GUID == "guid-1" {
			blocked = detail.Decision.ID
		}
	}
	if err := search.BlocklistRelease(t.Context(), item.ID, blocked, env.clock.Now()); err != nil {
		t.Fatalf("BlocklistRelease: %v", err)
	}

	second, err := search.SearchItem(t.Context(), item.ID, conns, env.clock.Now())
	if err != nil || len(second.Decisions) != 1 || second.Decisions[0].ReleaseCandidate.GUID != "guid-2" {
		t.Errorf("second search = %+v, %v; want only guid-2", second.Decisions, err)
	}
}

// "Search with different terms" searches exactly those terms, and a release named by them matches.
func TestSearchItemWithTerms_SearchesThePersonsOwnTerms(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.createSearchableWork(t, "Nosi Zada", "Discovery", 2001, alwaysAcceptRules)
	searcher := &fakeIndexerSearcher{responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
		return []indexer.RawItem{rawItem("guid-1", "Dekotu Bangalter-Trax on da Rocks-1995-FLAC-GROUP")}, nil
	}}

	result, err := env.search().SearchItemWithTerms(t.Context(), item.ID,
		[]music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now(),
		&music.SearchTerms{Artist: "Dekotu Bangalter", Album: "Trax on da Rocks"})
	if err != nil {
		t.Fatalf("SearchItemWithTerms: %v", err)
	}
	want := []indexer.SearchRequest{
		{Function: "music", Extra: map[string]string{"artist": "Dekotu Bangalter", "album": "Trax on da Rocks"}, Limit: 20},
		{Function: "music", Query: "Dekotu Bangalter Trax on da Rocks", Limit: 20},
	}
	if !reflect.DeepEqual(searcher.requests, want) {
		t.Errorf("requests = %+v, want %+v", searcher.requests, want)
	}
	// Both queries returned the same guid: one decision, not two.
	if len(result.Decisions) != 1 {
		t.Errorf("%d decisions, want the release matching the typed terms once", len(result.Decisions))
	}
}
