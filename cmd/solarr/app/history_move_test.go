package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// The download history jobs.db held moves to statistics.db once: other events stay, a second copy adds nothing,
// and an event written after the first copy follows on the next (revision 2, D7).
func TestCopyDownloadHistory_MovesTheDownloadEventsOnce(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	ctx := t.Context()
	log := jobs.NewHistory(server.process.jobs, clock.NewSystem())
	appendEvent := func(kind download.HistoryKind, album string) {
		t.Helper()
		data, err := json.Marshal(download.HistoryData{AcquisitionID: 7, CategoryID: ids.music, ReleaseTitle: album + ".FLAC", Album: album, Artist: "Amber Lanterns", Indexer: "Example"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := log.Append(ctx, jobs.Event{Kind: string(kind), SubjectType: "work", SubjectID: ids.album, Data: string(data)}); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(download.HistoryGrabbed, "Quiet Orchards")
	appendEvent(download.HistoryImported, "Quiet Orchards")
	if _, err := log.Append(ctx, jobs.Event{Kind: "backup.finished", Data: "{}"}); err != nil {
		t.Fatal(err)
	}

	history := func() []string {
		t.Helper()
		var rows struct {
			Items []struct {
				Kind   string `json:"kind"`
				Album  string `json:"album"`
				WorkID int64  `json:"work_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal(server.get(t, "/history?limit=50"), &rows); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, row := range rows.Items {
			got = append(got, fmt.Sprintf("%s %s %d", row.Kind, row.Album, row.WorkID))
		}
		return got
	}
	for range 2 {
		if err := server.process.copyDownloadHistory(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := fmt.Sprintf("imported Quiet Orchards %d,grabbed Quiet Orchards %d", ids.album, ids.album)
	if got := strings.Join(history(), ","); got != want {
		t.Errorf("history after copying twice = %s, want %s", got, want)
	}

	appendEvent(download.HistoryFailed, "Quiet Orchards")
	if err := server.process.copyDownloadHistory(ctx); err != nil {
		t.Fatal(err)
	}
	if got := history(); len(got) != 3 || !strings.HasPrefix(got[0], "failed ") {
		t.Errorf("history after a later event = %v, want it on top", got)
	}
}

// The statistics library.db's rollup held reach the statistics page through statistics.db once, and a later copy
// leaves statistics.db's own figures alone.
func TestCopyStatRollup_MovesTheFiguresOnce(t *testing.T) {
	server := newGoldenServer(t)
	ids := server.seedLibraries(t)
	ctx := t.Context()
	set := func(state string, count int64) {
		t.Helper()
		err := server.process.library.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			return librarydb.New(tx).SetStatRollup(ctx, librarydb.SetStatRollupParams{
				Metric: library.StatAlbumsByState, Dimension: fmt.Sprintf("%d|%s", ids.music, state), Value: float64(count),
				Count: sql.NullInt64{Int64: count, Valid: true}, ComputedAt: 5_000,
			})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	set("COMPLETE", 12)
	if err := server.process.copyStatRollup(ctx); err != nil {
		t.Fatal(err)
	}
	set("COMPLETE", 99)
	if err := server.process.copyStatRollup(ctx); err != nil {
		t.Fatal(err)
	}
	page := string(server.get(t, fmt.Sprintf("/categories/%d/statistics", ids.music)))
	if !strings.Contains(page, `"albums_by_state":[{"count":12,"key":"COMPLETE"}]`) {
		t.Errorf("statistics = %s, want the copied 12 complete albums, not the later 99", page)
	}
}
