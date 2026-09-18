package music

import (
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

func TestTrackPosition(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		disc, track, discCount int
		want                   string
	}{
		{"single disc keeps the library's own shape", 1, 6, 1, "06"},
		{"multi-disc prefixes the disc", 1, 6, 3, "1-06"},
		{"multi-disc, later disc", 3, 12, 3, "3-12"},
		{"unknown disc on a multi-disc release stays bare", 0, 6, 3, "06"},
	} {
		if got := TrackPosition(tc.disc, tc.track, tc.discCount); got != tc.want {
			t.Errorf("%s: TrackPosition(%d,%d,%d) = %q, want %q", tc.name, tc.disc, tc.track, tc.discCount, got, tc.want)
		}
	}
}

func TestDiscCount(t *testing.T) {
	parts := func(refs ...string) []library.PartDetail {
		details := make([]library.PartDetail, len(refs))
		for i, ref := range refs {
			details[i] = library.PartDetail{Part: librarydb.WorkPart{MediaRef: sql.NullString{String: ref, Valid: true}.String}}
		}
		return details
	}
	if got := DiscCount(parts("disc1:track1", "disc1:track2", "disc2:track1")); got != 2 {
		t.Errorf("DiscCount = %d, want 2", got)
	}
	if got := DiscCount(parts("disc1:track1", "not-a-ref")); got != 1 {
		t.Errorf("DiscCount with an unparsable ref = %d, want 1", got)
	}
}
