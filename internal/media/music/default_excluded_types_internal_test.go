package music

import (
	"testing"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

func TestAFollowWithoutAnExplicitListLeavesOutDemosButOneWithAnEmptyListAdmitsThem(t *testing.T) {
	demo := musicbrainz.ReleaseGroup{PrimaryType: "Album", SecondaryTypes: []string{"Demo"}}
	if ParseSubscriptionFilters("{}").Admits(demo) {
		t.Error("a follow without a list admitted a demo")
	}
	if !ParseSubscriptionFilters(`{"exclude_secondary_types":[]}`).Admits(demo) {
		t.Error("a follow excluding nothing refused a demo")
	}
}
