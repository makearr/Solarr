package movie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

// pinProfile gives a work the profile the test added it with, as the add dialog does when a person picked one.
func pinProfile(t *testing.T, opened *store.Store, workID, profileID int64) {
	t.Helper()
	if err := library.NewDesired(opened).SetProfileOverrideForWork(t.Context(), workID, profileID); err != nil {
		t.Fatalf("SetProfileOverrideForWork: %v", err)
	}
}
