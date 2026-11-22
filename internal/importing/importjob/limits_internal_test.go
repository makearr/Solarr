package importjob

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/archive"
)

func TestLimits_TheAdministratorsLargestArchiveReplacesTheDefaultAndNothingElse(t *testing.T) {
	want := archive.DefaultLimits()
	if got := (&Job{}).limits(); got != want {
		t.Fatalf("limits = %+v, want the defaults %+v", got, want)
	}
	want.MaxTotalBytes = 5 << 30
	if got := (&Job{maxArchive: func() int64 { return 5 << 30 }}).limits(); got != want {
		t.Fatalf("limits = %+v, want only MaxTotalBytes changed: %+v", got, want)
	}
}
