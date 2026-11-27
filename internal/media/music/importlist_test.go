package music_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
)

func TestResolveListEntry_AddsAReleaseGroupByItsMusicBrainzID(t *testing.T) {
	id := media.ProviderID{Provider: music.ProviderReleaseGroup, ExternalID: "rg-1"}
	got, err := music.Module{}.ResolveListEntry(t.Context(), media.ListEntry{IDs: []media.ProviderID{id}})
	if err != nil || !reflect.DeepEqual(got, media.ListMatch{ProviderID: id, Found: true}) {
		t.Errorf("got %+v, %v", got, err)
	}
	if got, _ := (music.Module{}).ResolveListEntry(t.Context(), media.ListEntry{IDs: []media.ProviderID{{Provider: "tmdb", ExternalID: "1"}}}); got.Found {
		t.Error("an entry without a release group was found")
	}
}
