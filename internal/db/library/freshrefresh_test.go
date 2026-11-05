package library_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// A work with a wanted item released in the last 48 hours or the next 7 days is fresh and comes first; the window's
// edges are exclusive of a release three days ago or a fortnight ahead.
func TestDueForRefreshOf_FreshWorksComeFirstAndAreMarked(t *testing.T) {
	opened, _ := newLibrary(t)
	now := time.Unix(10_000_000, 0)
	released := map[string]time.Time{
		"Long ago":   now.Add(-3 * 24 * time.Hour),
		"Aired":      now.Add(-24 * time.Hour),
		"Next week":  now.Add(6 * 24 * time.Hour),
		"Far ahead":  now.Add(14 * 24 * time.Hour),
		"No release": {},
	}
	ids := map[int64]string{}
	for _, title := range []string{"Long ago", "Far ahead", "No release", "Aired", "Next week"} {
		work := createWork(t, opened, "tv", title)
		ids[work.ID] = title
		item := createDesired(t, opened, work.ID, "SINGLE", 1)
		if released[title].IsZero() {
			continue
		}
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.UpsertHuntQueueEntry(ctx, librarydb.UpsertHuntQueueEntryParams{
				DesiredItemID: item.ID, Class: "missing", ReleaseDate: sql.NullInt64{Int64: released[title].Unix(), Valid: true},
			})
		})
	}

	due, err := library.NewSeriesReader(opened).DueForRefreshOf(t.Context(), "tv", "ended", now, time.Hour, time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRefreshOf: %v", err)
	}
	var got []string
	for _, candidate := range due {
		if candidate.Fresh {
			got = append(got, ids[candidate.WorkID])
		}
	}
	if len(due) != 5 || len(got) != 2 || due[0].Fresh != true || due[1].Fresh != true {
		t.Errorf("due = %+v, fresh titles %v; want five works, the two fresh ones first", due, got)
	}
}

func TestArtistsWithFreshRelease_OnlyMonitoredArtistsWithAWantedAlbumInTheWindow(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	now := time.Unix(10_000_000, 0)
	artistWithAlbum := func(name string, monitored int64, releasedAt time.Time) int64 {
		person := resolvePersonNamed(t, people, name, name)
		if err := queries.SetPersonMonitored(t.Context(), librarydb.SetPersonMonitoredParams{Monitored: monitored, ID: person}); err != nil {
			t.Fatal(err)
		}
		work := createWork(t, opened, "music", name+" album")
		item := createDesired(t, opened, work.ID, "SINGLE", 1)
		err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO credit (person_id, work_id, role) VALUES (?, ?, 'artist')`, person, work.ID); err != nil {
				return err
			}
			return librarydb.New(opened.Read()).WithTx(tx).UpsertHuntQueueEntry(ctx, librarydb.UpsertHuntQueueEntryParams{
				DesiredItemID: item.ID, Class: "missing", ReleaseDate: sql.NullInt64{Int64: releasedAt.Unix(), Valid: true},
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		return person
	}
	fresh := artistWithAlbum("Fresh", 1, now.Add(-time.Hour))
	artistWithAlbum("Old", 1, now.Add(-30*24*time.Hour))
	artistWithAlbum("Unmonitored", 0, now.Add(-time.Hour))

	got, err := people.ArtistsWithFreshRelease(t.Context(), now)
	if err != nil || len(got) != 1 || !got[fresh] {
		t.Errorf("fresh artists = %v, %v; want only %d", got, err, fresh)
	}
}
