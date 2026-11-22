package settings_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestRankProvisionalLastKeepsEachGroupsOrder(t *testing.T) {
	got := settings.RankProvisionalLast([]string{"local:jellyfin", "wikidata", "tmdb", "local:stash", "tvdb"})
	want := []string{"wikidata", "tmdb", "tvdb", "local:jellyfin", "local:stash"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEveryLocalProviderIsProvisionalAndPaced(t *testing.T) {
	for _, provider := range settings.MetadataProviders {
		if settings.IsProvisionalSource(provider.Key) != provider.Provisional {
			t.Errorf("%s: provisional flag and key prefix disagree", provider.Key)
		}
		if provider.Provisional && (provider.PerMinute != 600 || provider.Published != "your own server") {
			t.Errorf("%s: a local provider is paced 600 a minute and published as your own server", provider.Key)
		}
	}
}

func TestEnrichProvidersTakeNoPlaceInAnOrder(t *testing.T) {
	for _, provider := range settings.MetadataProviders {
		if provider.InOrder() != (len(provider.MediaTypes) > 0) {
			t.Errorf("%s: role and media types disagree", provider.Key)
		}
	}
}
