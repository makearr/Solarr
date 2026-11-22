package library_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func seedWatch(t *testing.T) library.Playback {
	t.Helper()
	opened, _ := newLibrary(t)
	statements := []string{
		`INSERT INTO work (id, media_type, canonical_title, sort_title, added_at, updated_at) VALUES (1, 'tv', 'Show One', 'show one', 1, 1), (2, 'tv', 'Show Two', 'show two', 1, 1)`,
		`INSERT INTO work_part (id, work_id, media_ref, sort_key) VALUES (11, 1, 's01e01', '1'), (12, 1, 's01e02', '2'), (21, 2, 's01e01', '1')`,
		`INSERT INTO media_object (id, size_bytes, quick_hash, first_seen_at) VALUES (31, 100, 'a', 1), (32, 200, 'b', 1), (33, 50, 'c', 1)`,
		`INSERT INTO library_path (media_object_id, root_folder_id, rel_path, link_kind, created_at) VALUES (31, 7, 'one/e1.mkv', 'copy', 1), (32, 7, 'one/e2.mkv', 'copy', 1), (33, 7, 'two/e1.mkv', 'copy', 1)`,
		`INSERT INTO work_file (work_id, media_object_id, first_seen_at) VALUES (1, 31, 1), (1, 32, 1), (2, 33, 1)`,
	}
	for _, statement := range statements {
		if _, err := opened.WriteDB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return library.NewPlayback(opened)
}

func TestPlayback_AWorkIsWatchedWhenEveryPartWasPlayedAndOneUserFinishingIsNotEveryUser(t *testing.T) {
	playback := seedWatch(t)
	state := func(user string, work, part int64, played bool, last int64) library.PlaybackState {
		return library.PlaybackState{ServerID: 1, ServerUserID: user, WorkID: work, PartID: part, Played: played, LastPlayedAt: last, UpdatedAt: 1}
	}
	err := playback.Upsert(t.Context(), []library.PlaybackState{
		state("ada", 1, 11, true, 100), state("ada", 1, 12, true, 200), // ada finished show one
		state("bo", 1, 11, true, 150),                               // bo only began it
		state("ada", 2, 0, true, 300), state("bo", 2, 0, true, 400), // both finished show two as a whole
	})
	if err != nil {
		t.Fatal(err)
	}
	both := []library.PlaybackPair{{ServerID: 1, ServerUserID: "ada"}, {ServerID: 1, ServerUserID: "bo"}}

	watch, err := playback.WatchOf(t.Context(), both, []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]library.WorkWatch{1: {Finished: 1, Started: 2, LastPlayedAt: 200}, 2: {Finished: 2, Started: 2, LastPlayedAt: 400}}
	if !reflect.DeepEqual(watch, want) {
		t.Fatalf("watch = %+v", watch)
	}

	cleanup, err := playback.WatchedWorks(t.Context(), library.WatchedQuery{Pairs: both, LastPlayedBefore: 1000, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(cleanup) != 1 || cleanup[0].ID != 2 || cleanup[0].SizeBytes != 50 || cleanup[0].FileCount != 1 {
		t.Fatalf("cleanup = %+v", cleanup)
	}
	recent, _ := playback.WatchedWorks(t.Context(), library.WatchedQuery{Pairs: both, LastPlayedBefore: 399, Limit: 10})
	if len(recent) != 0 {
		t.Fatalf("a play after the cutoff keeps the work off the list: %+v", recent)
	}
	for status, ids := range map[string][]int64{library.WatchedYes: {2}, library.WatchedInProgress: {1}, library.WatchedNo: nil} {
		rows, err := library.NewWorks(playback.Store()).List(t.Context(), library.WorkFilter{Watched: status, WatchPairs: both[1:]}, library.WorkCursor{}, 10)
		if err != nil {
			t.Fatal(err)
		}
		var got []int64
		for _, row := range rows {
			got = append(got, row.ID)
		}
		if !reflect.DeepEqual(got, ids) {
			t.Fatalf("watched=%s lists %v, want %v", status, got, ids)
		}
	}
	adaOnly, _ := playback.WatchedWorks(t.Context(), library.WatchedQuery{Pairs: both[:1], LastPlayedBefore: 1000, Limit: 10})
	if len(adaOnly) != 2 {
		t.Fatalf("ada alone finished both: %+v", adaOnly)
	}
}

func TestPlayback_ResolveFileFindsTheWorkOfAPath(t *testing.T) {
	playback := seedWatch(t)
	hit, found, err := playback.ResolveFile(t.Context(), 7, "one/e2.mkv")
	if err != nil || !found || hit.WorkID != 1 {
		t.Fatalf("hit %+v found %v err %v", hit, found, err)
	}
	if _, found, _ := playback.ResolveFile(t.Context(), 7, "nothing.mkv"); found {
		t.Fatal("a path the library does not hold resolves to nothing")
	}
}
