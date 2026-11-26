package apiserver

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

func TestIgnoredItemsAreLeftOutOfCollectionsAndCrossoversButAHeldWorkStays(t *testing.T) {
	provider, held, ignoredID, otherID := "tmdb", int64(5), "11", "12"
	ignored := map[string]bool{"tmdb:11": true}
	franchise := gen.Franchise{Members: []gen.FranchiseMember{
		{Title: "Held", WorkId: &held, Provider: &provider, ExternalId: &ignoredID},
		{Title: "Ignored", Provider: &provider, ExternalId: &ignoredID},
		{Title: "Other", Provider: &provider, ExternalId: &otherID},
	}}
	got := withoutIgnoredMembers(franchise, ignored)
	if titles := []string{got.Members[0].Title, got.Members[1].Title}; len(got.Members) != 2 || !reflect.DeepEqual(titles, []string{"Held", "Other"}) {
		t.Errorf("members = %+v, want the held work and the one not ignored", got.Members)
	}
	event := gen.Crossover{Missing: []gen.CrossoverMissingPart{{Provider: "tmdb", ExternalId: "11", Title: "a"}, {Provider: "tmdb", ExternalId: "12", Title: "b"}}}
	if parts := withoutIgnoredParts(event, ignored).Missing; len(parts) != 1 || parts[0].Title != "b" {
		t.Errorf("missing = %+v, want only the part not ignored", parts)
	}
	if untouched := withoutIgnoredMembers(franchise, nil); len(untouched.Members) != 3 {
		t.Errorf("with nothing ignored members = %d, want 3", len(untouched.Members))
	}
}
