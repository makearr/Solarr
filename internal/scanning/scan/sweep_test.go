package scan_test

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/scanning/scan"
)

const root = "/media/music"

type tree struct {
	fsys  *fsx.Mem
	clock *clock.Fake
}

func newTree(t *testing.T, files ...string) *tree {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	lib := &tree{fsys: fsx.NewMem(fake), clock: fake}
	if err := lib.fsys.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, file := range files {
		lib.write(t, file)
	}
	return lib
}

// write creates a file and every directory above it, advancing the clock first so
// each change lands at a distinct time -- which is what a real filesystem does.
func (l *tree) write(t *testing.T, relPath string) {
	t.Helper()
	l.clock.Advance(time.Second)
	absolute := path.Join(root, relPath)
	if err := l.fsys.MkdirAll(path.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path.Dir(absolute), err)
	}
	file, err := l.fsys.Create(absolute)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolute, err)
	}
	if _, err := file.Write([]byte(relPath)); err != nil {
		t.Fatalf("Write(%q): %v", absolute, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", absolute, err)
	}
}

func (l *tree) remove(t *testing.T, relPath string) {
	t.Helper()
	l.clock.Advance(time.Second)
	if err := l.fsys.Remove(path.Join(root, relPath)); err != nil {
		t.Fatalf("Remove(%q): %v", relPath, err)
	}
}

// collector records what a sweep emitted, which is what every assertion here is
// about.
type collector struct {
	found     []string
	batches   []scan.Batch
	failAfter int
	failure   error
}

func (c *collector) emit(_ context.Context, batch scan.Batch) error {
	if c.failure != nil && len(c.batches) >= c.failAfter {
		return c.failure
	}
	c.batches = append(c.batches, batch)
	for _, file := range batch.Files {
		c.found = append(c.found, file.RelPath)
	}
	return nil
}

func (c *collector) sorted() []string {
	found := slices.Clone(c.found)
	slices.Sort(found)
	return found
}

func sweep(t *testing.T, lib *tree, state scan.State, options scan.Options) (scan.Result, *collector) {
	t.Helper()
	// A sweep happens after the writes it is looking for, never in the same instant.
	lib.clock.Advance(time.Second)
	collected := &collector{}
	result, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), root, state, options, collected.emit)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	return result, collected
}

func TestSweep_FirstPassFindsEveryFileAtEveryDepth(t *testing.T) {
	lib := newTree(t,
		"loose.flac",
		"Nick Cave/Tender Prey/01.flac",
		"Nick Cave/Tender Prey/02.flac",
		"Nick Cave/Tender Prey/scans/front.jpg",
		"Bokuf Lipad/Rupe of Fofu/01.flac",
	)

	result, collected := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	want := []string{
		"Bokuf Lipad/Rupe of Fofu/01.flac",
		"Nick Cave/Tender Prey/01.flac",
		"Nick Cave/Tender Prey/02.flac",
		"Nick Cave/Tender Prey/scans/front.jpg",
		"loose.flac",
	}
	if !slices.Equal(collected.sorted(), want) {
		t.Errorf("found %v, want %v", collected.sorted(), want)
	}
	if !result.Completed {
		t.Error("a sweep that ran to the end did not report completion")
	}
	if result.FilesFound != len(want) {
		t.Errorf("FilesFound = %d, want %d", result.FilesFound, len(want))
	}
	// A completed sweep has no unfinished business.
	if result.State.Cursor != "" {
		t.Errorf("Cursor = %q after completing, want empty", result.State.Cursor)
	}
}

// The workhorse of section 10.4: at 500k files a full rescan is not an answer, so a
// second sweep must read only what changed.
func TestSweep_SecondPassReadsOnlyDirectoriesThatChanged(t *testing.T) {
	lib := newTree(t,
		"Nick Cave/Tender Prey/01.flac",
		"Bokuf Lipad/Rupe of Fofu/01.flac",
	)
	first, _ := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	lib.write(t, "Nick Cave/Tender Prey/02.flac")
	second, collected := sweep(t, lib, first.State, scan.Options{})

	// Only the directory that gained a file is read, and the whole of it: the
	// mtime says the listing changed, not which entry.
	want := []string{"Nick Cave/Tender Prey/01.flac", "Nick Cave/Tender Prey/02.flac"}
	if !slices.Equal(collected.sorted(), want) {
		t.Errorf("found %v, want only the changed directory's files %v", collected.sorted(), want)
	}
	if second.DirectoriesRead != 1 {
		t.Errorf("read %d directories, want 1", second.DirectoriesRead)
	}
	// The whole tree is still traversed: a directory's mtime moves when its own
	// entries change, never when a grandchild's do.
	if second.DirectoriesVisited != first.DirectoriesVisited {
		t.Errorf("visited %d directories, want the same %d as the first pass",
			second.DirectoriesVisited, first.DirectoriesVisited)
	}
}

// A sweep at 500k files takes minutes, and the library is live throughout. A
// directory written after the walk has already passed it must be caught by the next
// sweep -- a watermark taken from the newest mtime seen, or from when the sweep
// finished, would leave it behind forever.
func TestSweep_CatchesADirectoryWrittenWhileTheSweepWasRunning(t *testing.T) {
	lib := newTree(t, "a/01.flac", "b/01.flac")
	lib.clock.Advance(time.Second)

	// Emitting "a" is the moment the walk has passed it. Writing into it then is
	// exactly the race.
	collected := &collector{}
	first, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), root, scan.State{},
		scan.Options{Full: true}, func(ctx context.Context, batch scan.Batch) error {
			if batch.Directory == "a" {
				lib.write(t, "a/02.flac")
				// The sweep goes on to b/ afterwards, so the write is strictly
				// before the sweep ends -- which is what a watermark taken at the
				// end would swallow.
				lib.clock.Advance(time.Second)
			}
			return collected.emit(ctx, batch)
		})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !slices.Contains(collected.sorted(), "a/01.flac") {
		t.Fatalf("the first sweep did not read a/: %v", collected.sorted())
	}

	_, second := sweep(t, lib, first.State, scan.Options{})
	if !slices.Contains(second.sorted(), "a/02.flac") {
		t.Errorf("the second sweep found %v, missing the file written mid-sweep", second.sorted())
	}
}

func TestSweep_WithNothingChangedReadsNothingAtAll(t *testing.T) {
	lib := newTree(t, "Nick Cave/Tender Prey/01.flac")
	first, _ := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	second, collected := sweep(t, lib, first.State, scan.Options{})
	if len(collected.found) != 0 {
		t.Errorf("found %v on an unchanged library, want nothing", collected.found)
	}
	if second.DirectoriesRead != 0 {
		t.Errorf("read %d directories on an unchanged library", second.DirectoriesRead)
	}
	if second.DirectoriesVisited == 0 {
		t.Error("visited nothing: the tree must still be traversed to reach a changed grandchild")
	}
}

// A deletion changes the directory's listing too, so the sweep has to surface the
// directory -- deciding a path has gone is the caller's job, and section 10.4 is
// explicit that it is MISSING, never an immediate deletion.
func TestSweep_RereadsADirectoryAfterADeletion(t *testing.T) {
	lib := newTree(t,
		"Nick Cave/Tender Prey/01.flac",
		"Nick Cave/Tender Prey/02.flac",
	)
	first, _ := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	lib.remove(t, "Nick Cave/Tender Prey/02.flac")
	_, collected := sweep(t, lib, first.State, scan.Options{})

	if !slices.Equal(collected.sorted(), []string{"Nick Cave/Tender Prey/01.flac"}) {
		t.Errorf("found %v, want the surviving file so the caller can see 02 has gone",
			collected.sorted())
	}
}

// The walk visits a directory's whole subtree before its next sibling, so the order
// is "a", "a/c", "a-b" -- while as plain strings "a-b" < "a/c", because '-' is below
// '/'. A cursor compared as a string therefore skips the sibling entirely.
func TestSweep_DoesNotSkipASiblingThatSortsInsideAnEarlierSubtree(t *testing.T) {
	lib := newTree(t,
		"a/01.flac",
		"a/c/01.flac",
		"a-b/01.flac",
	)

	_, collected := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	want := []string{"a-b/01.flac", "a/01.flac", "a/c/01.flac"}
	if !slices.Equal(collected.sorted(), want) {
		t.Errorf("found %v, want %v", collected.sorted(), want)
	}
}

// The same hazard on the resume path: an interrupted sweep must not lose the sibling
// it had not reached yet.
func TestSweep_ResumesPastASubtreeWithoutLosingTheNextSibling(t *testing.T) {
	lib := newTree(t,
		"a/01.flac",
		"a/c/01.flac",
		"a-b/01.flac",
	)

	interrupted := errors.New("shutting down")
	collected := &collector{failAfter: 2, failure: interrupted}
	lib.clock.Advance(time.Second)
	result, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), root, scan.State{},
		scan.Options{Full: true}, collected.emit)
	if !errors.Is(err, interrupted) {
		t.Fatalf("Sweep = %v, want the caller's error", err)
	}
	if result.State.Cursor != "a/c" {
		t.Fatalf("Cursor = %q, want a/c", result.State.Cursor)
	}

	_, resumed := sweep(t, lib, result.State, scan.Options{Full: true})
	if !slices.Equal(resumed.sorted(), []string{"a-b/01.flac"}) {
		t.Errorf("resumed with %v, want the sibling that had not been reached", resumed.sorted())
	}
}

func TestSweep_ReportsSizeAndModificationTimeFromTheWalk(t *testing.T) {
	lib := newTree(t, "a/01.flac")
	_, collected := sweep(t, lib, scan.State{}, scan.Options{Full: true})

	if len(collected.found) != 1 {
		t.Fatalf("found %v, want one file", collected.found)
	}
	found := collected.batches[0].Files[0]
	if found.SizeBytes != int64(len("a/01.flac")) {
		t.Errorf("SizeBytes = %d, want %d", found.SizeBytes, len("a/01.flac"))
	}
	if found.ModTime.IsZero() {
		t.Error("ModTime is zero: stat verification has nothing to compare against")
	}
}

func TestSweep_SkipsWhatIncludeRejects(t *testing.T) {
	lib := newTree(t,
		"Nick Cave/Tender Prey/01.flac",
		"Nick Cave/Tender Prey/cover.jpg",
		"Nick Cave/Tender Prey/notes.txt",
	)
	_, collected := sweep(t, lib, scan.State{}, scan.Options{
		Full:    true,
		Include: func(relPath string) bool { return strings.HasSuffix(relPath, ".flac") },
	})

	if !slices.Equal(collected.sorted(), []string{"Nick Cave/Tender Prey/01.flac"}) {
		t.Errorf("found %v, want only the .flac", collected.sorted())
	}
}

func TestSweep_SplitsALargeDirectoryIntoBoundedBatches(t *testing.T) {
	var files []string
	for index := range 25 {
		files = append(files, fmt.Sprintf("box set/%02d.flac", index))
	}
	lib := newTree(t, files...)

	_, collected := sweep(t, lib, scan.State{}, scan.Options{Full: true, BatchSize: 10})

	if len(collected.batches) != 3 {
		t.Fatalf("got %d batches for 25 files at 10 a batch, want 3", len(collected.batches))
	}
	for index, batch := range collected.batches {
		if batch.Directory != "box set" {
			t.Errorf("batch %d names %q, want the directory it came from", index, batch.Directory)
		}
	}
	if len(collected.found) != 25 {
		t.Errorf("found %d files across the batches, want 25", len(collected.found))
	}
}

// A scan across 500k files is a job with progress, not a function call: a restart
// continues from the last committed batch (section 10.4, ADR-3).
func TestSweep_ResumesAfterTheCallerStoppedIt(t *testing.T) {
	lib := newTree(t,
		"a/01.flac",
		"b/01.flac",
		"c/01.flac",
	)

	interrupted := errors.New("the process is shutting down")
	collected := &collector{failAfter: 2, failure: interrupted}
	result, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), root, scan.State{},
		scan.Options{Full: true}, collected.emit)
	if !errors.Is(err, interrupted) {
		t.Fatalf("Sweep = %v, want the caller's error", err)
	}
	if result.Completed {
		t.Error("an interrupted sweep reported completion, so the watermark would move")
	}
	if !slices.Equal(collected.sorted(), []string{"a/01.flac", "b/01.flac"}) {
		t.Fatalf("committed %v before stopping", collected.sorted())
	}

	// The cursor is the last directory *committed*, so the resumed sweep starts
	// after it and does not redo work, nor skip any.
	_, resumed := sweep(t, lib, result.State, scan.Options{Full: true})
	if !slices.Equal(resumed.sorted(), []string{"c/01.flac"}) {
		t.Errorf("resumed with %v, want only what was left", resumed.sorted())
	}
}

// The cursor means "everything up to here is durable". Advancing it for a batch the
// caller rejected would silently drop that batch on restart.
func TestSweep_DoesNotAdvanceThePastABatchTheCallerRefused(t *testing.T) {
	lib := newTree(t, "a/01.flac", "b/01.flac")

	refused := errors.New("write queue closed")
	collected := &collector{failAfter: 1, failure: refused}
	result, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), root, scan.State{},
		scan.Options{Full: true}, collected.emit)
	if !errors.Is(err, refused) {
		t.Fatalf("Sweep = %v, want the caller's error", err)
	}

	_, resumed := sweep(t, lib, result.State, scan.Options{Full: true})
	if !slices.Contains(resumed.sorted(), "b/01.flac") {
		t.Errorf("resumed with %v: the refused batch was lost", resumed.sorted())
	}
}

// A directory whose files were all filtered out is still swept, or a resume would
// read it again forever.
func TestSweep_AdvancesPastADirectoryWithNothingWorthEmitting(t *testing.T) {
	lib := newTree(t, "a/notes.txt", "b/01.flac")

	result, collected := sweep(t, lib, scan.State{}, scan.Options{
		Full:    true,
		Include: func(relPath string) bool { return strings.HasSuffix(relPath, ".flac") },
	})
	if !slices.Equal(collected.sorted(), []string{"b/01.flac"}) {
		t.Fatalf("found %v", collected.sorted())
	}
	if !result.Completed {
		t.Error("the sweep did not complete")
	}
}

func TestSweep_StopsWhenTheContextIsCancelled(t *testing.T) {
	lib := newTree(t, "a/01.flac", "b/01.flac")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	collected := &collector{}
	result, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(ctx, root, scan.State{},
		scan.Options{Full: true}, collected.emit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Sweep = %v, want context.Canceled", err)
	}
	if result.Completed {
		t.Error("a cancelled sweep reported completion")
	}
}

func TestSweep_RefusesARelativeRoot(t *testing.T) {
	lib := newTree(t)
	_, err := scan.NewSweeper(lib.fsys, lib.clock).Sweep(t.Context(), "music", scan.State{},
		scan.Options{}, (&collector{}).emit)
	if err == nil {
		t.Error("a relative root was accepted")
	}
}

// An unmounted disk must not look like the loss of an entire library. The sweep
// reports nothing and fails nothing; whether the root is online is settings.db's
// answer, and only that turns absence into deletion (section 10.4).
func TestSweep_TreatsAnAbsentRootAsNothingFoundRatherThanAFailure(t *testing.T) {
	lib := newTree(t)
	if err := lib.fsys.Remove(root); err != nil {
		t.Fatalf("Remove(root): %v", err)
	}

	result, collected := sweep(t, lib, scan.State{}, scan.Options{Full: true})
	if len(collected.found) != 0 {
		t.Errorf("found %v under a root that is not mounted", collected.found)
	}
	if result.DirectoriesVisited != 0 {
		t.Errorf("visited %d directories under an absent root", result.DirectoriesVisited)
	}
}
