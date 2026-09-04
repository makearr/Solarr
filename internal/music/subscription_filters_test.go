package music_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

func TestSubscriptionFilters_DefaultExcludesLiveCompilationRemixDJMixAndMixtape(t *testing.T) {
	filters := music.ParseSubscriptionFilters("")

	admitted := musicbrainz.ReleaseGroup{Title: "KI Pecezinu"}
	if !filters.Admits(admitted) {
		t.Error("want a plain album admitted")
	}

	for _, secondary := range []string{"Compilation", "Live", "Remix", "DJ-mix", "Mixtape/Street"} {
		rg := musicbrainz.ReleaseGroup{Title: secondary + " release", SecondaryTypes: []string{secondary}}
		if filters.Admits(rg) {
			t.Errorf("want %q excluded by default, got admitted", secondary)
		}
	}
}

func TestSubscriptionFilters_EmptyStoredFiltersStillDefaultExcludeRatherThanAdmitEverything(t *testing.T) {
	filters := music.ParseSubscriptionFilters("{}")
	rg := musicbrainz.ReleaseGroup{Title: "Some Bootleg", SecondaryTypes: []string{"Live"}}

	if filters.Admits(rg) {
		t.Error("an empty stored filter must still default-exclude, not admit everything")
	}
}

func TestSubscriptionFilters_ExplicitExcludeListOverridesTheDefault(t *testing.T) {
	filters := music.ParseSubscriptionFilters(`{"exclude_secondary_types":["soundtrack"]}`)

	live := musicbrainz.ReleaseGroup{Title: "Live Album", SecondaryTypes: []string{"Live"}}
	if !filters.Admits(live) {
		t.Error("an explicit exclude list replaces the default -- live should be admitted here")
	}

	soundtrack := musicbrainz.ReleaseGroup{Title: "Movie OST", SecondaryTypes: []string{"Soundtrack"}}
	if filters.Admits(soundtrack) {
		t.Error("the explicit exclude list should still exclude soundtrack")
	}
}

func TestSubscriptionFilters_SoundtrackIsAdmittedByDefault(t *testing.T) {
	filters := music.ParseSubscriptionFilters("")
	rg := musicbrainz.ReleaseGroup{Title: "Movie OST", SecondaryTypes: []string{"Soundtrack"}}

	if !filters.Admits(rg) {
		t.Error("soundtrack is not in the default exclude list and should be admitted")
	}
}
