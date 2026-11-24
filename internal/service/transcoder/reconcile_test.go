package transcoder_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

const workFile = "/lib/recycle-bin/.transcode/1.0.m4a"

// crashed prepares an item whose replace stopped at the given stage; recycled says the original already went to the bin.
func crashed(t *testing.T, stage string, recycled, outputThere, finalThere bool) *pipeline {
	t.Helper()
	p := musicPipeline(t)
	var entryID int64
	if recycled {
		entry, err := p.bin.Put(context.Background(), library.PutRequest{RootPath: "/lib", OriginalPath: "/lib/Album/t1.flac"})
		if err != nil {
			t.Fatal(err)
		}
		entryID = entry.ID
	}
	if outputThere {
		p.writeFile(t, workFile, 800_000)
	}
	if finalThere {
		p.writeFile(t, "/lib/Album/t1.m4a", 800_000)
	}
	_ = p.items.SetJournal(context.Background(), 1, stage, workFile, entryID)
	return p
}

// libraryHoldsOneFile asserts the path the library knows exists on disk, and that the other of the two does not.
func libraryHoldsOneFile(t *testing.T, p *pipeline, wantRel string) {
	t.Helper()
	known := p.items.paths[1][0].RelPath
	other := "Album/t1.flac"
	if wantRel == other {
		other = "Album/t1.m4a"
	}
	if known != wantRel || !p.exists("/lib/"+known) || p.exists("/lib/"+other) {
		t.Errorf("library knows %q (on disk %v), other %q on disk %v; want exactly %q", known, p.exists("/lib/"+known), other, p.exists("/lib/"+other), wantRel)
	}
}

func TestReconcile_ReplaceStoppedBeforeTheOriginalLeftGoesBackToWaiting(t *testing.T) {
	p := crashed(t, "recycling", false, true, false)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeNeeded || item.ReplaceStage != "" || item.Attempts != 0 {
		t.Errorf("item = %+v, want needed with a clear journal", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.flac")
	if p.exists(workFile) {
		t.Error("the work file nobody names must go")
	}
}

func TestReconcile_ReplaceStoppedAfterRecyclingFinishesPlacingTheResult(t *testing.T) {
	p := crashed(t, "placing", true, true, false)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeDone || item.ReplaceStage != "" {
		t.Errorf("item = %+v, want done with a clear journal", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.m4a")
	if !p.exists("/lib/recycle-bin/day/1-t1.flac") {
		t.Error("the original must stay in the bin")
	}
}

func TestReconcile_ReplaceStoppedAfterPlacingOnlyRecordsTheNewBytes(t *testing.T) {
	p := crashed(t, "placing", true, false, true)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeDone {
		t.Errorf("item = %+v, want done", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.m4a")
}

func TestReconcile_WhenTheResultIsGoneTheOriginalComesBackFromTheBin(t *testing.T) {
	p := crashed(t, "placing", true, false, false)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeNeeded || item.ReplaceStage != "" || !item.Interrupted {
		t.Errorf("item = %+v, want needed and interrupted with a clear journal", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.flac")
}

func TestReconcile_ARunningItemWithoutAJobIsInterruptedAndStrayWorkFilesAreRemoved(t *testing.T) {
	p := musicPipeline(t)
	p.writeFile(t, "/lib/recycle-bin/.transcode/99.0.m4a", 10)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded || !item.Interrupted || item.Attempts != 0 {
		t.Errorf("item = %+v, want needed, interrupted, no attempt", item)
	}
	if p.exists("/lib/recycle-bin/.transcode/99.0.m4a") {
		t.Error("a work file no journal names was left")
	}
}

func TestReconcile_ReplaceStoppedBeforeTheBinEntryWasNotedFindsItByTheOriginalPath(t *testing.T) {
	p := crashed(t, "recycling", true, true, false)
	_ = p.items.SetJournal(context.Background(), 1, "recycling", workFile, 0)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeDone || item.OriginalRecycleID != 1 {
		t.Errorf("item = %+v, want done with the bin entry it found", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.m4a")
}
