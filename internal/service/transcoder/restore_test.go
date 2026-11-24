package transcoder_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// converted is a pipeline whose one track went through a conversion: the flac is bin entry 1 and the m4a sits in the library.
func converted(t *testing.T) *pipeline {
	t.Helper()
	p := musicPipeline(t)
	p.outputs(t, []int{800_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if p.item(1).State != library.TranscodeDone {
		t.Fatalf("the conversion did not finish: %+v", p.item(1))
	}
	return p
}

func TestRestore_PutsTheOriginalBackAndRestsTheItemAndSubtractsTheSavings(t *testing.T) {
	p := converted(t)
	if err := p.svc.Restore(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeNotNeeded || item.OriginalRecycleID != 0 || item.ReplaceStage != "" || !strings.Contains(item.ReasonsJSON, `"restored_by_user"`) {
		t.Errorf("item = %+v, want a resting item with the restored_by_user reason and no journal", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.flac")
	if len(p.items.restored) != 1 || p.items.restored[0].SizeBytes != 3_000_000 || p.items.restored[0].ResultRelPath != "Album/t1.m4a" {
		t.Errorf("restored = %+v, want the original's size and the result's path", p.items.restored)
	}
	if entry := p.bin.entries[2]; entry.Reason != library.ReasonTranscodeReverted || entry.ExpiresAt != 24*3600 || !strings.HasSuffix(entry.OriginalPath, "t1.m4a") {
		t.Errorf("entry 2 = %+v, want the converted file kept under the library's retention", entry)
	}
	if len(p.reported.reverted) != 1 || p.reported.reverted[0].OriginalBytes != 3_000_000 || p.reported.reverted[0].ResultBytes != 800_000 || p.reported.reverted[0].CategoryID != 3 {
		t.Errorf("reverted = %+v, want the savings of the conversion taken back", p.reported.reverted)
	}
}

func TestRestore_TheOriginalsRetentionIsTheEntrysLifetime(t *testing.T) {
	p := musicPipeline(t)
	p.settings.global.Originals.RetainDays = 7
	p.outputs(t, []int{800_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if got := p.bin.entries[1].ExpiresAt; got != 7*24*3600 {
		t.Errorf("original's retention = %d seconds, want 7 days", got)
	}
}

func TestRestore_IsRefusedWhenTheOriginalIsGoneOrTheFileIsNotConvertedOrItsPlaceIsTaken(t *testing.T) {
	p := converted(t)
	if err := p.bin.Purge(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if err := p.svc.Restore(context.Background(), 1); !errors.Is(err, transcoder.ErrOriginalGone) {
		t.Errorf("after the purge: %v, want ErrOriginalGone", err)
	}
	if item := p.item(1); item.State != library.TranscodeDone || !p.exists("/lib/Album/t1.m4a") {
		t.Errorf("a refused restore changed something: %+v", item)
	}

	forgotten := converted(t)
	_ = forgotten.items.SetJournal(context.Background(), 1, "", "", 0)
	if err := forgotten.svc.Restore(context.Background(), 1); !errors.Is(err, transcoder.ErrOriginalGone) {
		t.Errorf("an item that lost its entry: %v, want ErrOriginalGone", err)
	}

	waiting := musicPipeline(t)
	if err := waiting.svc.Restore(context.Background(), 1); !errors.Is(err, transcoder.ErrNotRestorable) {
		t.Errorf("a file never converted: %v, want ErrNotRestorable", err)
	}

	taken := converted(t)
	taken.writeFile(t, "/lib/Album/t1.flac", 10)
	if err := taken.svc.Restore(context.Background(), 1); !errors.Is(err, transcoder.ErrOriginalBlocked) {
		t.Errorf("original's place taken: %v, want ErrOriginalBlocked", err)
	}
	if !taken.exists("/lib/Album/t1.m4a") || taken.item(1).ReplaceStage != "" {
		t.Error("a refused restore must leave the converted file and no journal")
	}
}

// interruptedRestore is a conversion whose restore stopped after the given number of its steps: 0 = journal only, 1 = converted file in
// the bin, 2 = original back on disk.
func interruptedRestore(t *testing.T, steps int) *pipeline {
	t.Helper()
	p := converted(t)
	if err := p.items.SetJournal(context.Background(), 1, "restoring", "", 1); err != nil {
		t.Fatal(err)
	}
	if steps >= 1 {
		if _, err := p.bin.Put(context.Background(), library.PutRequest{RootPath: "/lib", OriginalPath: "/lib/Album/t1.m4a", Reason: library.ReasonTranscodeReverted}); err != nil {
			t.Fatal(err)
		}
	}
	if steps >= 2 {
		if _, err := p.bin.Restore(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func TestReconcile_ARestoreThatStoppedBeforeAnythingMovedIsForgotten(t *testing.T) {
	p := interruptedRestore(t, 0)
	if err := p.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeDone || item.ReplaceStage != "" || item.OriginalRecycleID != 1 {
		t.Errorf("item = %+v, want done with a clear journal and its original", item)
	}
	libraryHoldsOneFile(t, p, "Album/t1.m4a")
}

func TestReconcile_ARestoreThatStoppedAtEachLaterStepIsFinished(t *testing.T) {
	for steps := 1; steps <= 2; steps++ {
		p := interruptedRestore(t, steps)
		if err := p.svc.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if item := p.item(1); item.State != library.TranscodeNotNeeded || item.ReplaceStage != "" {
			t.Errorf("after step %d: item = %+v, want restored with a clear journal", steps, item)
		}
		libraryHoldsOneFile(t, p, "Album/t1.flac")
		if len(p.reported.reverted) != 1 {
			t.Errorf("after step %d: savings reverted %d times, want once", steps, len(p.reported.reverted))
		}
	}
}

func keptOriginals(p *pipeline, sizes ...int64) {
	p.bin.entries = map[int64]library.Entry{}
	for i, size := range sizes {
		id := int64(i + 1)
		p.bin.entries[id] = library.Entry{ID: id, Reason: library.ReasonTranscoded, SizeBytes: size, RecycledPath: "/lib/recycle-bin/day/gone"}
	}
	p.bin.last = int64(len(sizes))
}

func TestEnforceCap_PurgesTheOldestUntilTheKeptOriginalsFit(t *testing.T) {
	const gigabyte = 1 << 30
	p := musicPipeline(t)
	p.settings.global.Originals.CapGB = 3
	keptOriginals(p, 2*gigabyte, 2*gigabyte, 2*gigabyte)
	if err := p.svc.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, oldestKept := p.bin.entries[1]; oldestKept || len(p.bin.entries) != 1 || p.bin.entries[3].ID != 3 {
		t.Errorf("entries left = %v, want only the newest", p.bin.entries)
	}
}

func TestEnforceCap_StopsTheMomentTheKeptOriginalsEqualTheCap(t *testing.T) {
	const gigabyte = 1 << 30
	p := musicPipeline(t)
	p.settings.global.Originals.CapGB = 2
	keptOriginals(p, 2*gigabyte, gigabyte, gigabyte)
	if err := p.svc.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.bin.entries) != 2 {
		t.Errorf("entries = %v, want the oldest purged and the 2 GB left kept", p.bin.entries)
	}
}

func TestEnforceCap_ExactlyAtTheCapPurgesNothingAndNoCapKeepsEverything(t *testing.T) {
	const gigabyte = 1 << 30
	p := musicPipeline(t)
	p.settings.global.Originals.CapGB = 3
	keptOriginals(p, gigabyte, 2*gigabyte)
	if err := p.svc.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.settings.global.Originals.CapGB = 0
	keptOriginals(p, 9*gigabyte)
	if err := p.svc.EnforceCap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(p.bin.entries) != 1 {
		t.Errorf("entries = %v, want the single entry of the second case kept", p.bin.entries)
	}
	keptOriginals(p, gigabyte, 2*gigabyte)
	p.settings.global.Originals.CapGB = 3
	_ = p.svc.EnforceCap(context.Background())
	if len(p.bin.entries) != 2 {
		t.Errorf("entries = %v, want both kept at exactly the cap", p.bin.entries)
	}
}
