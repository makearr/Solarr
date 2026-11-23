package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestANewItemOfALibraryWithoutAProfileTakesItsMediaTypesProfile(t *testing.T) {
	own := int64(7)
	monitor := WorksMonitor{DefaultProfileID: 1, ProfileFor: func(settings.Category, int64) int64 { return 23 }}
	if got := monitor.profileOf(settings.Category{QualityProfileID: &own}); got != 7 {
		t.Fatalf("a library's own profile = %d, want 7", got)
	}
	if got := monitor.profileOf(settings.Category{}); got != 23 {
		t.Fatalf("a library with none = %d, want its media type's 23", got)
	}
	if got := (WorksMonitor{DefaultProfileID: 1}).profileOf(settings.Category{}); got != 1 {
		t.Fatalf("no lookup = %d, want the default 1", got)
	}
}
