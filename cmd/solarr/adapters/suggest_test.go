package adapters_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/screen"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func newSuggestLibrary(t *testing.T) *store.Store {
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

// A new work is suggested the library whose rule it matches most fully, a genre below the rule's matching, a library
// whose rating it exceeds left out, a library's own rules over its kind's (plan 10-libraries L5).
func TestSuggester_NamesTheLibraryAWorkBelongsIn(t *testing.T) {
	cache := newSweepCache(t)
	opened := newSuggestLibrary(t)
	people := library.NewPeople(opened)
	if err := people.SeedGenreTree(t.Context(), screen.GenreTree.Key, 1, []library.GenreTreeTag{
		{Name: "Animation", Children: []library.GenreTreeTag{{Name: "Anime"}}}, {Name: "Family"}, {Name: "Drama"}, {Name: "Documentary"},
	}); err != nil {
		t.Fatal(err)
	}
	registry, err := media.NewRegistry(tv.Module{})
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]int64{}
	for _, entry := range []struct{ name, kind string }{{"Series", "series"}, {"Anime", "anime"}, {"Kids", "kids"}} {
		category, err := cache.CreateCategory(t.Context(), "tv", entry.name, 10)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cache.SetCategoryKind(t.Context(), category.ID, entry.kind); err != nil {
			t.Fatal(err)
		}
		created[entry.name] = category.ID
	}
	seven := int64(7)
	if _, err := cache.SetCategoryRatingLimit(t.Context(), created["Kids"], &seven, false); err != nil {
		t.Fatal(err)
	}
	suggester := adapters.Suggester{Registry: registry, Settings: cache, People: people, Works: library.NewWorks(opened)}
	suggest := func(traits media.Traits) string {
		t.Helper()
		suggestion, found, err := suggester.ForCandidate(t.Context(), "tv", traits)
		if err != nil {
			t.Fatal(err)
		}
		if !found {
			return ""
		}
		for name, id := range created {
			if id == suggestion.CategoryID {
				return name + ": " + suggestion.Reason
			}
		}
		return "?"
	}

	for name, want := range map[string]struct {
		traits media.Traits
		want   string
	}{
		"animation in Japanese": {media.Traits{Genres: []string{"Animation"}, OriginalLanguage: "jpn"}, "Anime: Animation, original language ja"},
		"anime from Japan":      {media.Traits{Genres: []string{"Anime"}, OriginCountries: []string{"JP"}}, "Anime: Animation, from JP"},
		"family for the young":  {media.Traits{Genres: []string{"Family"}, Rating: "TV-Y"}, "Kids: Family"},
		"family for the older":  {media.Traits{Genres: []string{"Family"}, Rating: "TV-14"}, ""},
		"a drama":               {media.Traits{Genres: []string{"Drama"}}, ""},
	} {
		if got := suggest(want.traits); got != want.want {
			t.Errorf("%s: suggested %q, want %q", name, got, want.want)
		}
	}

	if _, err := cache.SetCategoryRules(t.Context(), created["Series"], []settings.LibraryRule{
		{Conditions: []settings.RuleCondition{{Field: settings.RuleGenre, Value: "Drama"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := suggest(media.Traits{Genres: []string{"Drama"}}); got != "Series: Drama" {
		t.Errorf("a drama with Series' own rule: %q, want Series", got)
	}
}

// A library's check names its works that belong elsewhere and the kind of library its works ask for that is missing.
func TestSuggester_ChecksALibrary(t *testing.T) {
	cache := newSweepCache(t)
	opened := newSuggestLibrary(t)
	people := library.NewPeople(opened)
	if err := people.SeedGenreTree(t.Context(), screen.GenreTree.Key, 1, []library.GenreTreeTag{{Name: "Animation"}, {Name: "Documentary"}}); err != nil {
		t.Fatal(err)
	}
	registry, err := media.NewRegistry(tv.Module{})
	if err != nil {
		t.Fatal(err)
	}
	series, _ := cache.CreateCategory(t.Context(), "tv", "Series", 10)
	anime, _ := cache.CreateCategory(t.Context(), "tv", "Anime", 10)
	if _, err := cache.SetCategoryKind(t.Context(), anime.ID, "anime"); err != nil {
		t.Fatal(err)
	}
	works := library.NewWorks(opened)
	for _, entry := range []struct {
		title, language, genre string
	}{{"Cartoon", "ja", "Animation"}, {"Nature", "en", "Documentary"}} {
		write := library.SeriesWrite{
			Work: library.WorkIdentity{MediaType: "tv", CanonicalTitle: entry.title, SortTitle: entry.title}, OriginalLanguage: entry.language,
			IDs: []library.ExternalIDWrite{{Provider: "tvdb", ExternalID: entry.title}}, Genres: []string{entry.genre}, GenreSource: "tmdb", GenreTree: "screen",
		}
		result, err := library.NewSeries(opened).Upsert(t.Context(), clock.NewFakeAtEpoch().Now(), write)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := library.NewDesired(opened).EnsureSeasonItem(t.Context(), library.SeasonRequest{WorkID: result.WorkID, MediaRef: "s01", CategoryID: series.ID, ProfileID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	check, err := adapters.Suggester{Registry: registry, Settings: cache, People: people, Works: works}.Check(t.Context(), series.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(check.Misplaced) != 1 || check.Misplaced[0].Title != "Cartoon" || check.Misplaced[0].Suggestion.CategoryID != anime.ID {
		t.Errorf("misplaced = %+v, want the cartoon toward Anime", check.Misplaced)
	}
	if want := []apiserver.MissingKind{{MediaType: "tv", Kind: "documentary", Label: "Documentary", Works: 1}}; !reflect.DeepEqual(check.MissingKinds, want) {
		t.Errorf("missing kinds = %+v, want %+v", check.MissingKinds, want)
	}
}
