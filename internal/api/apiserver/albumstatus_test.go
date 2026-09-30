package apiserver_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

// The four colours the review asked for, and the order they outrank each
// other in (2026-09-19).
func TestAlbumStatus_DecidesTheColourInPrecedenceOrder(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)
	complete, missing := "COMPLETE", "MISSING"

	cases := []struct {
		name string
		row  apiserver.PersonWorkRow
		want string
	}{
		{
			name: "an album known only by its own tags is unverified, whatever else holds (D6)",
			row:  apiserver.PersonWorkRow{Unverified: true, OnDisk: true, WantedState: &complete},
			want: "unverified",
		},
		{
			name: "not out yet is never missing, even while downloading",
			row:  apiserver.PersonWorkRow{AvailableFrom: &future, Downloading: true, WantedState: &missing},
			want: "unreleased",
		},
		{
			name: "a download in flight shows even over a complete album being upgraded",
			row:  apiserver.PersonWorkRow{AvailableFrom: &past, Downloading: true, WantedState: &complete},
			want: "downloading",
		},
		{
			name: "a complete wanted album is green",
			row:  apiserver.PersonWorkRow{WantedState: &complete},
			want: "complete",
		},
		{
			name: "files with nothing wanting them are green too",
			row:  apiserver.PersonWorkRow{OnDisk: true},
			want: "complete",
		},
		{
			name: "wanted and not complete is red",
			row:  apiserver.PersonWorkRow{WantedState: &missing},
			want: "incomplete",
		},
		{
			name: "nothing wants it and nothing is on disk",
			row:  apiserver.PersonWorkRow{},
			want: "incomplete",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := apiserver.AlbumStatusForTest(test.row, now); got != test.want {
				t.Errorf("albumStatus = %q, want %q", got, test.want)
			}
		})
	}
}
