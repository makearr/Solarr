package adapters

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestAutomaticTargetOf_IsTheFirstProfilesAverageAndNothingWithoutAProfile(t *testing.T) {
	if _, derived := automaticTargetOf(&settings.Snapshot{}, nil, "music"); derived {
		t.Error("no profile chosen: want no target")
	}
	if _, derived := automaticTargetOf(&settings.Snapshot{}, []int64{99}, "music"); derived {
		t.Error("an unknown profile: want no target")
	}
}
