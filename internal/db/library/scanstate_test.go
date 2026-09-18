package library_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

var (
	sweepStart  = time.Unix(1_700_000_000, 0).UTC()
	sweepFinish = time.Unix(1_700_000_600, 0).UTC()
)

func TestScanProgress_ARootNeverSweptIsNotAnError(t *testing.T) {
	opened, _ := newLibrary(t)

	progress, err := library.NewScans(opened).Progress(t.Context(), 7)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	// The zero value is a first run, which every root has exactly once.
	if progress.RootFolderID != 7 || !progress.SweptThrough.IsZero() || progress.Cursor != "" {
		t.Errorf("progress = %+v, want an empty state for root 7", progress)
	}
	if progress.Running() {
		t.Error("a root that has never been swept reported a sweep in progress")
	}
}

// The watermark is what says a directory need not be read again, so a sweep that
// merely started must not move it.
func TestScanStart_RecordsTheAttemptWithoutMovingTheWatermark(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)

	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	progress, err := scans.Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if !progress.SweptThrough.IsZero() {
		t.Errorf("SweptThrough = %s after only starting", progress.SweptThrough)
	}
	if !progress.StartedAt.Equal(sweepStart) {
		t.Errorf("StartedAt = %s, want %s", progress.StartedAt, sweepStart)
	}
	if !progress.Running() {
		t.Error("a started sweep did not report as running")
	}
}

func TestScanFinish_MovesTheWatermarkAndClearsTheCursor(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)

	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := scans.Advance(t.Context(), 1, "Nick Cave/Tender Prey"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	err := scans.Finish(t.Context(), 1, library.FinishedScan{
		SweptThrough: sweepStart, FinishedAt: sweepFinish,
		FilesFound: 412, DirectoriesRead: 19,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	progress, err := scans.Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if !progress.SweptThrough.Equal(sweepStart) {
		t.Errorf("SweptThrough = %s, want the sweep's start %s", progress.SweptThrough, sweepStart)
	}
	// A completed sweep has nothing to resume.
	if progress.Cursor != "" {
		t.Errorf("Cursor = %q after completing", progress.Cursor)
	}
	if progress.Running() {
		t.Error("a finished sweep still reported as running")
	}
	if progress.FilesFound != 412 || progress.DirectoriesRead != 19 {
		t.Errorf("counters = %d/%d, want 412/19", progress.FilesFound, progress.DirectoriesRead)
	}
}

// After a restart an interrupted sweep is exactly a started one with a cursor and no
// finish, and the cursor is what makes it resumable rather than restarted.
func TestScanAdvance_SurvivesAsAResumePointWhenTheSweepNeverFinished(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)

	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, cursor := range []string{"a", "a/c", "a-b"} {
		if err := scans.Advance(t.Context(), 1, cursor); err != nil {
			t.Fatalf("Advance(%q): %v", cursor, err)
		}
	}

	progress, err := scans.Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if progress.Cursor != "a-b" {
		t.Errorf("Cursor = %q, want the last one committed", progress.Cursor)
	}
	if !progress.SweptThrough.IsZero() {
		t.Errorf("SweptThrough = %s: an interrupted sweep moved the watermark", progress.SweptThrough)
	}
	if !progress.Running() {
		t.Error("an interrupted sweep did not report as running, so a restart would not resume it")
	}
}

// Starting again must not throw away the cursor a previous run left, or a restart
// would rescan the whole root instead of resuming.
func TestScanStart_KeepsTheCursorOfAnInterruptedSweep(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)

	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := scans.Advance(t.Context(), 1, "Nick Cave"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	restarted := sweepStart.Add(time.Hour)
	if err := scans.Start(t.Context(), 1, restarted); err != nil {
		t.Fatalf("Start again: %v", err)
	}

	progress, err := scans.Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if progress.Cursor != "Nick Cave" {
		t.Errorf("Cursor = %q, want the interrupted run's resume point", progress.Cursor)
	}
	if !progress.StartedAt.Equal(restarted) {
		t.Errorf("StartedAt = %s, want the new attempt's %s", progress.StartedAt, restarted)
	}
	if !progress.FinishedAt.IsZero() {
		t.Errorf("FinishedAt = %s on a sweep that just started", progress.FinishedAt)
	}
}

// A second sweep after a completed one starts clean rather than resuming a cursor
// that no longer means anything.
func TestScanStart_AfterACompletedSweepKeepsTheWatermarkAndTheEmptyCursor(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)

	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	err := scans.Finish(t.Context(), 1, library.FinishedScan{
		SweptThrough: sweepStart, FinishedAt: sweepFinish,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if err := scans.Start(t.Context(), 1, sweepFinish.Add(time.Hour)); err != nil {
		t.Fatalf("Start again: %v", err)
	}

	progress, err := scans.Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	if !progress.SweptThrough.Equal(sweepStart) {
		t.Errorf("SweptThrough = %s, want the earlier sweep's watermark kept", progress.SweptThrough)
	}
	if progress.Cursor != "" {
		t.Errorf("Cursor = %q, want nothing to resume", progress.Cursor)
	}
}

func TestScanAll_ListsEveryRootWithProgress(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)
	for _, root := range []int64{3, 1} {
		if err := scans.Start(t.Context(), root, sweepStart); err != nil {
			t.Fatalf("Start(%d): %v", root, err)
		}
	}

	all, err := scans.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 || all[0].RootFolderID != 1 || all[1].RootFolderID != 3 {
		t.Errorf("All = %+v, want roots 1 and 3 in order", all)
	}
}

func TestScanForget_RemovesARootThatHasLeftTheConfiguration(t *testing.T) {
	opened, _ := newLibrary(t)
	scans := library.NewScans(opened)
	if err := scans.Start(t.Context(), 1, sweepStart); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := scans.Forget(t.Context(), 1); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	all, err := scans.All(t.Context())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("All = %+v after forgetting the only root", all)
	}
}
