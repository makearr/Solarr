package book

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestResolveListEntry_AddsAWorkByItsOpenLibraryID(t *testing.T) {
	module := Module{Format: Ebook}
	id := media.ProviderID{Provider: ProviderOpenLibrary, ExternalID: "OL1W"}
	got, err := module.ResolveListEntry(t.Context(), media.ListEntry{IDs: []media.ProviderID{id}})
	if err != nil || !reflect.DeepEqual(got, media.ListMatch{ProviderID: id, Found: true}) {
		t.Errorf("got %+v, %v", got, err)
	}
	if got, _ := module.ResolveListEntry(t.Context(), media.ListEntry{IDs: []media.ProviderID{{Provider: ProviderOpenLibrary, ExternalID: "OL1M"}}}); got.Found {
		t.Error("an edition id was taken for a work")
	}
}
