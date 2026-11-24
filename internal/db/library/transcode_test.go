package library_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// seedTranscode inserts files 1..6: id, size, first_seen_at and path chosen so every order differs.
func seedTranscode(t *testing.T) (library.TranscodeItems, *store.Store) {
	t.Helper()
	opened, _ := newLibrary(t)
	statements := []string{
		`INSERT INTO media_object (id, size_bytes, quick_hash, first_seen_at, kind) VALUES
		  (1, 300, 'a', 50, 'video'), (2, 100, 'b', 40, 'video'), (3, 200, 'c', 30, 'video'), (4, 200, 'd', 20, 'video'), (5, 50, 'e', 10, 'audio'), (6, 10, 'f', 5, 'book')`,
		`INSERT INTO library_path (media_object_id, root_folder_id, rel_path, link_kind, is_primary, created_at) VALUES
		  (1, 7, 'show b/e1.mkv', 'copy', 1, 1), (2, 7, 'show a/e1.mkv', 'copy', 1, 1), (3, 7, 'show a/e2.mkv', 'copy', 1, 1),
		  (4, 7, 'show c/e1.mkv', 'copy', 1, 1), (5, 8, 'album/t1.flac', 'copy', 1, 1), (6, 7, 'show c/book.epub', 'copy', 1, 1)`,
	}
	for _, statement := range statements {
		if _, err := opened.WriteDB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return library.NewTranscodeItems(opened, clock.NewFakeAtEpoch()), opened
}

func judge(id int64, kind string, state library.TranscodeState) library.JudgedTranscodeItem {
	return library.JudgedTranscodeItem{MediaObjectID: id, CategoryID: 1, TargetID: 9, SetupRevision: 2, Kind: kind, State: state, ReasonsJSON: "[]"}
}

func assertCountersMatchRows(t *testing.T, items library.TranscodeItems, opened *store.Store, category int64) {
	t.Helper()
	truth, err := items.Counts(t.Context(), category)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := library.NewCounters(opened).Counters(t.Context(), "category:1")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range library.TranscodeStates {
		if stored[library.TranscodeMetric(state)] != truth[state] {
			t.Errorf("counter %s = %d, rows say %d", state, stored[library.TranscodeMetric(state)], truth[state])
		}
	}
}

func TestTranscodeItems_JudgeCountsAndAReJudgeMovesTheCounterButNeverTouchesARunningItem(t *testing.T) {
	items, opened := seedTranscode(t)
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded), judge(2, "video", library.TranscodeNeeded), judge(5, "audio", library.TranscodeNotNeeded)}); err != nil {
		t.Fatal(err)
	}
	assertCountersMatchRows(t, items, opened, 1)
	if err := items.Transition(t.Context(), 1, library.TranscodeNeeded, library.TranscodeRunning, nil); err != nil {
		t.Fatal(err)
	}
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNotNeeded), judge(2, "video", library.TranscodeBlocked)}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := items.Get(t.Context(), 1)
	other, _, _ := items.Get(t.Context(), 2)
	if got.State != library.TranscodeRunning || other.State != library.TranscodeBlocked {
		t.Errorf("states = %s, %s; want running untouched and blocked", got.State, other.State)
	}
	assertCountersMatchRows(t, items, opened, 1)
}

func TestTranscodeItems_EveryTransitionKeepsTheCountersEqualToTheRows(t *testing.T) {
	items, opened := seedTranscode(t)
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)}); err != nil {
		t.Fatal(err)
	}
	steps := []struct{ from, to library.TranscodeState }{
		{library.TranscodeNeeded, library.TranscodeRunning}, {library.TranscodeRunning, library.TranscodeNeeded}, {library.TranscodeNeeded, library.TranscodeRunning},
		{library.TranscodeRunning, library.TranscodeFailed}, {library.TranscodeFailed, library.TranscodeNeeded}, {library.TranscodeNeeded, library.TranscodeRunning},
		{library.TranscodeRunning, library.TranscodeDone}, {library.TranscodeDone, library.TranscodeNeeded}, {library.TranscodeNeeded, library.TranscodeBlocked},
		{library.TranscodeBlocked, library.TranscodeNotNeeded},
	}
	for _, step := range steps {
		if err := items.Transition(t.Context(), 1, step.from, step.to, func(item *library.TranscodeItem) { item.Attempts++ }); err != nil {
			t.Fatalf("%s to %s: %v", step.from, step.to, err)
		}
		assertCountersMatchRows(t, items, opened, 1)
	}
	if got, _, _ := items.Get(t.Context(), 1); got.Attempts != int64(len(steps)) || got.State != library.TranscodeNotNeeded {
		t.Errorf("item = %+v", got)
	}
}

func TestTranscodeItems_TransitionRefusesAWrongStateAndAMissingItemAndChangesNothing(t *testing.T) {
	items, opened := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)})
	if err := items.Transition(t.Context(), 1, library.TranscodeRunning, library.TranscodeDone, nil); err == nil || !errorsIs(err, library.ErrTranscodeState) {
		t.Errorf("wrong from: %v", err)
	}
	if err := items.Transition(t.Context(), 99, library.TranscodeNeeded, library.TranscodeRunning, nil); err == nil || !errorsIs(err, library.ErrNoTranscodeItem) {
		t.Errorf("missing: %v", err)
	}
	assertCountersMatchRows(t, items, opened, 1)
}

func TestTranscodeItems_PickOrdersTieBreakByIdAndInterruptedComeFirst(t *testing.T) {
	items, _ := seedTranscode(t)
	var judged []library.JudgedTranscodeItem
	for _, id := range []int64{1, 2, 3, 4} {
		judged = append(judged, judge(id, "video", library.TranscodeNeeded))
	}
	if err := items.Judge(t.Context(), judged); err != nil {
		t.Fatal(err)
	}
	ids := func(order library.TranscodeOrder) []int64 {
		picks, err := items.Pick(t.Context(), order, 1, "video", 10)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, pick := range picks {
			out = append(out, pick.MediaObjectID)
		}
		return out
	}
	for order, want := range map[library.TranscodeOrder][]int64{
		library.OrderAlphabetical: {2, 3, 1, 4}, library.OrderLargest: {1, 3, 4, 2}, library.OrderSmallest: {2, 3, 4, 1},
		library.OrderOldest: {4, 3, 2, 1}, library.OrderNewest: {1, 2, 3, 4}, library.OrderFound: {1, 2, 3, 4},
	} {
		if got := ids(order); !reflect.DeepEqual(got, want) {
			t.Errorf("%s = %v, want %v", order, got, want)
		}
	}
	if err := items.Transition(t.Context(), 4, library.TranscodeNeeded, library.TranscodeNeeded, func(item *library.TranscodeItem) { item.Interrupted = true }); err != nil {
		t.Fatal(err)
	}
	if got := ids(library.OrderAlphabetical); got[0] != 4 {
		t.Errorf("interrupted item not first: %v", got)
	}
	far, err := items.PickFarAway(t.Context(), 1, "video", 7, "show c/", 10)
	if err != nil || len(far) != 3 || far[0].MediaObjectID != 1 || far[2].MediaObjectID != 2 {
		t.Errorf("far away = %+v, %v; want the far end outside show c", far, err)
	}
	if limited, _ := items.Pick(t.Context(), library.OrderAlphabetical, 1, "video", 2); len(limited) != 2 {
		t.Errorf("limit ignored: %d", len(limited))
	}
}

func TestTranscodeItems_ObjectsToAnalyzeSkipsJudgedRowsOfThisTargetAndPages(t *testing.T) {
	items, _ := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)})
	page, err := items.ObjectsToAnalyze(t.Context(), []int64{7, 8}, 9, 2, 0, 2)
	if err != nil || len(page) != 2 || page[0].MediaObjectID != 2 || page[1].MediaObjectID != 3 || page[0].RelPath != "show a/e1.mkv" {
		t.Fatalf("page = %+v, %v; want files 2 and 3 (1 is judged)", page, err)
	}
	next, _ := items.ObjectsToAnalyze(t.Context(), []int64{7, 8}, 9, 2, page[1].MediaObjectID, 10)
	if len(next) != 2 || next[0].MediaObjectID != 4 || next[1].Kind != "audio" {
		t.Errorf("next = %+v; want 4 and the audio file, never the book", next)
	}
	stale, _ := items.ObjectsToAnalyze(t.Context(), []int64{7}, 9, 3, 0, 10)
	if len(stale) != 4 || stale[0].MediaObjectID != 1 {
		t.Errorf("a new revision must re-judge file 1 too: %+v", stale)
	}
	if other, _ := items.ObjectsToAnalyze(t.Context(), []int64{8}, 9, 2, 0, 10); len(other) != 1 || other[0].MediaObjectID != 5 {
		t.Errorf("roots filter = %+v", other)
	}
}

func TestTranscodeItems_ListFiltersByStateKindAndSearchWithACursor(t *testing.T) {
	items, _ := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded), judge(2, "video", library.TranscodeFailed), judge(5, "audio", library.TranscodeNeeded)})
	list := func(filter library.TranscodeFilter) []int64 {
		filter.PageSize = 10
		rows, err := items.List(t.Context(), filter)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, row := range rows {
			out = append(out, row.Item.MediaObjectID)
		}
		return out
	}
	if got := list(library.TranscodeFilter{}); !reflect.DeepEqual(got, []int64{1, 2, 5}) {
		t.Errorf("all = %v", got)
	}
	if got := list(library.TranscodeFilter{State: library.TranscodeNeeded, Kind: "audio"}); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("needed audio = %v", got)
	}
	if got := list(library.TranscodeFilter{Search: "SHOW A"}); !reflect.DeepEqual(got, []int64{2}) {
		t.Errorf("search = %v", got)
	}
	if got := list(library.TranscodeFilter{AfterID: 2}); !reflect.DeepEqual(got, []int64{5}) {
		t.Errorf("cursor = %v", got)
	}
}

func TestTranscodeItems_ResetFailedMovesOnlyThatTargetsFailedItemsAndTheCounters(t *testing.T) {
	items, opened := seedTranscode(t)
	other := judge(3, "video", library.TranscodeFailed)
	other.TargetID = 4
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeFailed), judge(2, "video", library.TranscodeDone), other})
	_ = items.Transition(t.Context(), 1, library.TranscodeFailed, library.TranscodeFailed, func(item *library.TranscodeItem) { item.Attempts, item.FailureCode = 3, "error" })
	if err := items.ResetFailed(t.Context(), 9); err != nil {
		t.Fatal(err)
	}
	one, _, _ := items.Get(t.Context(), 1)
	three, _, _ := items.Get(t.Context(), 3)
	if one.State != library.TranscodeNeeded || one.Attempts != 0 || one.FailureCode != "" || three.State != library.TranscodeFailed {
		t.Errorf("one = %+v, three = %+v", one, three)
	}
	assertCountersMatchRows(t, items, opened, 1)
}

func TestTranscodeItems_ADeletedFileLeavesDriftThatReconcileCorrects(t *testing.T) {
	items, opened := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded), judge(2, "video", library.TranscodeNeeded)})
	if _, err := opened.WriteDB().ExecContext(t.Context(), `DELETE FROM media_object WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := items.Get(t.Context(), 2); ok {
		t.Fatal("the item survived its file")
	}
	corrections, err := items.ReconcileCounters(t.Context(), []library.ReconcileScope{{Scope: "category:1", CategoryID: 1}})
	if err != nil || corrections[0].Metric != "transcode.needed" || corrections[0].Was != 2 || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, %v; want needed corrected from 2 to 1 (never-written metrics are seeded at zero)", corrections, err)
	}
	assertCountersMatchRows(t, items, opened, 1)
}

func TestTranscodeItems_ResultJournalRunningAndHealthRoundTrip(t *testing.T) {
	items, opened := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)})
	_ = items.Transition(t.Context(), 1, library.TranscodeNeeded, library.TranscodeRunning, func(item *library.TranscodeItem) { item.LastRunSeq = 4; item.Lane = "cpu" })
	if err := items.SetJournal(t.Context(), 1, "placing", "/work/1.mp4", 0); err != nil {
		t.Fatal(err)
	}
	running, _ := items.Running(t.Context())
	journalled, _ := items.Journalled(t.Context())
	if len(running) != 1 || running[0].LastRunSeq != 4 || len(journalled) != 1 || journalled[0].ReplaceStage != "placing" || journalled[0].State != library.TranscodeRunning || journalled[0].WorkPath != "/work/1.mp4" {
		t.Fatalf("running = %+v, journalled = %+v", running, journalled)
	}
	if _, err := opened.WriteDB().ExecContext(t.Context(), `INSERT INTO recycle_bin (id, original_path, recycled_path, reason, size_bytes, recycled_at, expires_at, media_object_id) VALUES (3, '/a', '/r/a', 'transcoded', 700, 10, 99, 1), (4, '/b', '/r/b', 'transcoded', 300, 5, 99, NULL), (5, '/c', '/r/c', 'replaced', 900, 1, 99, NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := items.SetResult(t.Context(), 1, library.TranscodeResult{OriginalRecycleID: 3, OriginalSizeBytes: 700, ResultSizeBytes: 200, ResultRatioPct: 100, Engine: "software", EncodeSeconds: 60, FinishedAt: 77}); err != nil {
		t.Fatal(err)
	}
	if err := items.SetJournal(t.Context(), 1, "", "", 3); err != nil {
		t.Fatal(err)
	}
	got, _, _ := items.Get(t.Context(), 1)
	if got.ResultSizeBytes != 200 || got.OriginalRecycleID != 3 || got.FinishedAt != 77 || got.Engine != "software" || got.ReplaceStage != "" {
		t.Errorf("item = %+v", got)
	}
	if kept, _ := items.OriginalsKept(t.Context()); kept != 1000 {
		t.Errorf("kept = %d, want the two transcoded originals", kept)
	}
	if count, next, _ := items.OriginalsHeld(t.Context()); count != 2 || next != 99 {
		t.Errorf("held = %d next %d, want the two transcoded originals expiring at 99", count, next)
	}
	if expiries, _ := items.OriginalExpiries(t.Context(), []int64{3, 5, 77}); !reflect.DeepEqual(expiries, map[int64]int64{3: 99, 5: 99}) {
		t.Errorf("expiries = %v", expiries)
	}
	if oldest, _ := items.OldestOriginals(t.Context(), 1); len(oldest) != 1 || oldest[0].RecycleID != 4 {
		t.Errorf("oldest = %+v", oldest)
	}
	health, _ := items.Health(t.Context(), "video")
	if health.Mode != "ok" {
		t.Fatalf("default health = %+v", health)
	}
	want := library.TranscodeHealth{Kind: "video", ConsecutiveFailures: 3, Mode: "backoff", BackoffStep: 2, BackoffUntil: 5000, LastFailedID: 8, LastError: "boom", RunSeq: 12}
	if err := items.SaveHealth(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if got, _ := items.Health(t.Context(), "video"); got != want {
		t.Errorf("health = %+v, want %+v", got, want)
	}
}

func TestTranscodeItems_RestoreBytesPutsTheOriginalBackAndRestsTheItemInOneTransaction(t *testing.T) {
	items, opened := seedTranscode(t)
	_ = items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)})
	_ = items.Transition(t.Context(), 1, library.TranscodeNeeded, library.TranscodeRunning, nil)
	if err := items.ReplaceBytes(t.Context(), library.ReplacedFile{
		MediaObjectID: 1, RootFolderID: 7, OldRelPath: "show b/e1.mkv", NewRelPath: "show b/e1.mp4", SizeBytes: 120, QuickHash: "new",
		Result: library.TranscodeResult{OriginalSizeBytes: 300, ResultSizeBytes: 120, Engine: "software", FinishedAt: 99},
	}); err != nil {
		t.Fatal(err)
	}
	_ = items.SetJournal(t.Context(), 1, "restoring", "", 0)
	err := items.RestoreBytes(t.Context(), library.RestoredFile{
		MediaObjectID: 1, RootFolderID: 7, ResultRelPath: "show b/e1.mp4", OriginalRel: "show b/e1.mkv", SizeBytes: 300, QuickHash: "old", ReasonsJSON: `[{"code":"restored_by_user","stream":-1}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	var hash, path string
	read := opened.Read()
	_ = read.QueryRowContext(t.Context(), `SELECT size_bytes, quick_hash FROM media_object WHERE id = 1`).Scan(&size, &hash)
	_ = read.QueryRowContext(t.Context(), `SELECT rel_path FROM library_path WHERE media_object_id = 1`).Scan(&path)
	item, _, _ := items.Get(t.Context(), 1)
	if size != 300 || hash != "old" || path != "show b/e1.mkv" {
		t.Errorf("size %d hash %q path %q, want the original's", size, hash, path)
	}
	if item.State != library.TranscodeNotNeeded || item.ReplaceStage != "" || item.OriginalRecycleID != 0 || item.ResultSizeBytes != 0 || item.FinishedAt != 0 || item.ReasonsJSON != `[{"code":"restored_by_user","stream":-1}]` {
		t.Errorf("item = %+v, want a resting item with no result and no journal", item)
	}
	assertCountersMatchRows(t, items, opened, 1)
	if err := items.RestoreBytes(t.Context(), library.RestoredFile{MediaObjectID: 1, RootFolderID: 7, ResultRelPath: "x", OriginalRel: "x"}); !errorsIs(err, library.ErrTranscodeState) {
		t.Errorf("a second restore = %v, want a refusal", err)
	}
}

func errorsIs(err, target error) bool { return errors.Is(err, target) }

func TestTranscodeItems_FactsReadTheMeasuredAttributesAndFlagAFileNeverMeasured(t *testing.T) {
	items, opened := seedTranscode(t)
	statements := []string{
		`INSERT INTO media_attributes (id, attr_hash, video_codec, hdr, audio_codec, audio_channels, bit_depth, languages, subtitles, frame_rate, scan_type, container, sample_rate)
		 VALUES (1, 'h1', 'h265', 'hdr10', 'aac', '5.1', 10, '["en","de"]', '["fr"]', 23.976, 'progressive', 'matroska,webm', 48000)`,
		`UPDATE media_object SET measured_attributes_id = 1, duration_ms = 5000, width = 1920, height = 800, video_bitrate = 4000000, overall_bitrate = 4500000 WHERE id = 1`,
	}
	for _, statement := range statements {
		if _, err := opened.WriteDB().ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	got, err := items.Facts(t.Context(), []int64{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]library.ObjectFacts{
		1: {
			MediaObjectID: 1, SizeBytes: 300, DurationMs: 5000, Width: 1920, Height: 800, VideoBitrate: 4000000, OverallBitrate: 4500000, Measured: true,
			VideoCodec: "h265", HDR: "hdr10", AudioCodec: "aac", AudioChannels: "5.1", ScanType: "progressive", Container: "matroska,webm",
			BitDepth: 10, SampleRate: 48000, FrameRate: 23.976, AudioLanguages: []string{"en", "de"}, SubtitleLanguages: []string{"fr"},
		},
		2: {MediaObjectID: 2, SizeBytes: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Facts = %+v\nwant %+v", got, want)
	}
}

func TestTranscodeItems_ReplaceBytesGivesTheObjectItsNewBytesAndPathAndFinishesTheItemTogether(t *testing.T) {
	items, opened := seedTranscode(t)
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)}); err != nil {
		t.Fatal(err)
	}
	if err := items.Transition(t.Context(), 1, library.TranscodeNeeded, library.TranscodeRunning, nil); err != nil {
		t.Fatal(err)
	}
	err := items.ReplaceBytes(t.Context(), library.ReplacedFile{
		MediaObjectID: 1, RootFolderID: 7, OldRelPath: "show b/e1.mkv", NewRelPath: "show b/e1.mp4", SizeBytes: 120, QuickHash: "new",
		Result: library.TranscodeResult{OriginalSizeBytes: 300, ResultSizeBytes: 120, ResultRatioPct: 40, Engine: "software", FinishedAt: 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	var hash, path string
	read := opened.Read()
	if err := read.QueryRowContext(t.Context(), `SELECT size_bytes, quick_hash FROM media_object WHERE id = 1`).Scan(&size, &hash); err != nil {
		t.Fatal(err)
	}
	if err := read.QueryRowContext(t.Context(), `SELECT rel_path FROM library_path WHERE media_object_id = 1`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	item, _, _ := items.Get(t.Context(), 1)
	if size != 120 || hash != "new" || path != "show b/e1.mp4" || item.State != library.TranscodeDone || item.ResultSizeBytes != 120 || item.FinishedAt != 99 {
		t.Errorf("size %d hash %q path %q item %+v, want the converted bytes at the new path and a done item", size, hash, path, item)
	}
	assertCountersMatchRows(t, items, opened, 1)
}

func TestTranscodeItems_AFailedFileJudgedAgainstANewSetupRevisionStartsOverButTheSameRevisionKeepsItsAttempts(t *testing.T) {
	items, _ := seedTranscode(t)
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)}); err != nil {
		t.Fatal(err)
	}
	_ = items.Transition(t.Context(), 1, library.TranscodeNeeded, library.TranscodeRunning, nil)
	_ = items.Transition(t.Context(), 1, library.TranscodeRunning, library.TranscodeFailed, func(item *library.TranscodeItem) {
		item.Attempts, item.Pass, item.FailureCode = 3, 2, "error"
	})
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{judge(1, "video", library.TranscodeNeeded)}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := items.Get(t.Context(), 1); got.Attempts != 3 || got.Pass != 2 {
		t.Errorf("same revision: attempts %d pass %d, want 3 and 2 kept", got.Attempts, got.Pass)
	}
	again := judge(1, "video", library.TranscodeNeeded)
	again.SetupRevision = 3
	if err := items.Judge(t.Context(), []library.JudgedTranscodeItem{again}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := items.Get(t.Context(), 1); got.Attempts != 0 || got.Pass != 0 || got.FailureCode != "" || got.State != library.TranscodeNeeded {
		t.Errorf("new revision: %+v, want needed with attempts, pass and failure cleared", got)
	}
}
