package scan_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/migrations"
)

// fakeRoots stands in for settings.db, which is policy the scan asks about rather
// than reads (ADR-25).
type fakeRoots map[int64]settings.RootFolder

func (f fakeRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	root, found := f[id]
	return root, found
}

// fakeTool answers every path with the same tags unless told to fail.
type fakeTool struct {
	mu        sync.Mutex
	tags      mediatool.Tags
	failPath  string
	batchErr  error
	batchSize []int
}

func (f *fakeTool) Available(context.Context) error { return nil }

func (f *fakeTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	f.batchSize = append(f.batchSize, len(paths))
	probes := make([]mediatool.Probe, len(paths))
	for index, p := range paths {
		probes[index] = mediatool.Probe{Path: p, Tags: f.tags}
		if f.failPath != "" && p == f.failPath {
			probes[index] = mediatool.Probe{Path: p, Err: errors.New("moov atom not found")}
		}
	}
	return probes, nil
}

func (f *fakeTool) WriteTags(context.Context, []mediatool.TagEdit) ([]error, error) {
	return nil, nil
}

func (f *fakeTool) Fingerprint(context.Context, []string) ([]mediatool.Fingerprint, error) {
	return nil, nil
}

// recorder is the Consumer under test's stand-in for library.db.
// The scan observes several directories at once, so it is safe for concurrent use.
type recorder struct {
	mu              sync.Mutex
	seen            []scan.Observed
	seenCategoryIDs []int64
	batches         int
	// failDirectory's batch fails with failure; every other batch commits.
	failDirectory string
	failure       error
}

func (r *recorder) Observe(_ context.Context, _ settings.RootFolder, categoryID int64, batch []scan.Observed) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failure != nil && len(batch) > 0 && path.Dir(batch[0].File.RelPath) == r.failDirectory {
		return r.failure
	}
	r.batches++
	r.seen = append(r.seen, batch...)
	r.seenCategoryIDs = append(r.seenCategoryIDs, categoryID)
	return nil
}

// paths is every path seen, sorted: batches arrive in whatever order they finish.
func (r *recorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	found := make([]string, len(r.seen))
	for index, observed := range r.seen {
		found[index] = observed.File.RelPath
	}
	slices.Sort(found)
	return found
}

// jobFixture is a real library store, a real filesystem under a temporary directory,
// and fakes for everything the scan only asks questions of.
type jobFixture struct {
	root string
	// The walked filesystem is fsx.Mem, on the *same* fake clock as the sweep.
	// The sweep compares that clock against filesystem mtimes, so a fake clock
	// over the real filesystem makes every directory look newer than every
	// watermark. library.db is a real store; only the walk is in memory.
	fsys     *fsx.Mem
	store    *store.Store
	clock    *clock.Fake
	tool     *fakeTool
	recorder *recorder
	roots    fakeRoots
}

func newJobFixture(t *testing.T, files ...string) *jobFixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", backups, err)
	}

	const mediaRoot = "/media/music"
	fake := clock.NewFakeAtEpoch()
	memory := fsx.NewMem(fake)
	if err := memory.MkdirAll(mediaRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", mediaRoot, err)
	}
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fixture := &jobFixture{
		root: mediaRoot, fsys: memory, store: opened, clock: fake,
		tool:     &fakeTool{tags: mediatool.Tags{"artist": "Nick Cave"}},
		recorder: &recorder{},
		roots:    fakeRoots{1: {ID: 1, Path: mediaRoot, Label: "music", Online: true}},
	}
	for _, file := range files {
		fixture.write(t, file)
	}
	return fixture
}

// write creates a file and every directory above it, advancing the clock first so
// each change lands at a distinct time, as a real filesystem does.
func (f *jobFixture) write(t *testing.T, relPath string) {
	t.Helper()
	f.clock.Advance(time.Second)
	full := path.Join(f.root, relPath)
	if err := f.fsys.MkdirAll(path.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path.Dir(full), err)
	}
	file, err := f.fsys.Create(full)
	if err != nil {
		t.Fatalf("Create(%q): %v", full, err)
	}
	if _, err := file.Write([]byte(relPath)); err != nil {
		t.Fatalf("Write(%q): %v", full, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", full, err)
	}
}

func (f *jobFixture) job(t *testing.T, include func(string) bool) *scan.Job {
	t.Helper()
	return scan.NewJob(scan.Config{
		FS: f.fsys, Scans: library.NewScans(f.store), Roots: f.roots,
		Tool: f.tool, Consumer: f.recorder, Physical: library.NewPhysical(f.store),
		Clock: f.clock, Logger: slog.New(slog.DiscardHandler), Include: include,
	})
}

// stalePath inserts a library_path row directly, bypassing the sweep entirely, so a
// test can plant something for the mark-missing pass to find without it ever being a
// file the walk would see. lastSeenAt is Unix seconds.
func (f *jobFixture) stalePath(t *testing.T, relPath string, lastSeenAt int64) int64 {
	t.Helper()
	var id int64
	err := f.store.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(f.store.Read()).WithTx(tx)
		objectID, err := queries.CreateMediaObject(ctx, librarydb.CreateMediaObjectParams{
			SizeBytes: 1, QuickHash: relPath, FirstSeenAt: lastSeenAt,
		})
		if err != nil {
			return err
		}
		id, err = queries.AddLibraryPath(ctx, librarydb.AddLibraryPathParams{
			MediaObjectID: objectID, RootFolderID: 1, RelPath: relPath,
			LinkKind: "primary", IsPrimary: 1, CreatedAt: lastSeenAt, LastSeenAt: lastSeenAt,
		})
		return err
	})
	if err != nil {
		t.Fatalf("stalePath(%q): %v", relPath, err)
	}
	return id
}

// missingSince reads back whether one library_path is currently marked missing.
func (f *jobFixture) missingSince(t *testing.T, pathID int64) sql.NullInt64 {
	t.Helper()
	all, err := librarydb.New(f.store.Read()).ListPathsInRoot(t.Context(),
		librarydb.ListPathsInRootParams{RootFolderID: 1, AfterID: 0, PageSize: 1000})
	if err != nil {
		t.Fatalf("ListPathsInRoot: %v", err)
	}
	for _, row := range all {
		if row.ID == pathID {
			return row.MissingSince
		}
	}
	t.Fatalf("path %d not found", pathID)
	return sql.NullInt64{}
}

func (f *jobFixture) run(t *testing.T, handler *scan.Job, payload scan.Payload) error {
	t.Helper()
	// A sweep happens after the writes it is looking for, never in the same instant.
	f.clock.Advance(time.Second)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return handler.Run(t.Context(), jobs.Job{Type: scan.JobType, Payload: encoded})
}

func (f *jobFixture) progress(t *testing.T) library.ScanProgress {
	t.Helper()
	progress, err := library.NewScans(f.store).Progress(t.Context(), 1)
	if err != nil {
		t.Fatalf("Progress: %v", err)
	}
	return progress
}

func TestJob_SweepsProbesAndRecordsTheWatermark(t *testing.T) {
	fixture := newJobFixture(t,
		"Nick Cave/Tender Prey/01.flac",
		"Nick Cave/Tender Prey/02.flac",
		"Bokuf Lipad/Rupe of Fofu/01.flac",
	)
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fixture.recorder.seen) != 3 {
		t.Errorf("consumer saw %v, want three files", fixture.recorder.paths())
	}
	// Every file was probed, and the probe travelled with the file it belongs to.
	for _, observed := range fixture.recorder.seen {
		if observed.Probe.Err != nil {
			t.Errorf("%s carried a probe error: %v", observed.File.RelPath, observed.Probe.Err)
		}
		if observed.Probe.Tags["artist"] != "Nick Cave" {
			t.Errorf("%s carried no tags", observed.File.RelPath)
		}
		if !strings.HasSuffix(observed.Probe.Path, observed.File.RelPath) {
			t.Errorf("probe %q does not belong to %q", observed.Probe.Path, observed.File.RelPath)
		}
	}

	progress := fixture.progress(t)
	if progress.SweptThrough.IsZero() || progress.Running() {
		t.Errorf("progress = %+v, want a completed sweep", progress)
	}
	if progress.FilesFound != 3 {
		t.Errorf("FilesFound = %d, want 3", progress.FilesFound)
	}
}

// A finished sweep says so, so the library's counts catch up without waiting for their daily task.
func TestJob_TellsOnSweptAfterASweep(t *testing.T) {
	fixture := newJobFixture(t, "Nick Cave/Tender Prey/01.flac")
	told := 0
	handler := scan.NewJob(scan.Config{
		FS: fixture.fsys, Scans: library.NewScans(fixture.store), Roots: fixture.roots,
		Tool: fixture.tool, Consumer: fixture.recorder, Physical: library.NewPhysical(fixture.store),
		Clock: fixture.clock, Logger: slog.New(slog.DiscardHandler), Include: scan.IsAudio,
		OnSwept: func(context.Context) { told++ },
	})

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if told != 1 {
		t.Errorf("OnSwept called %d times, want 1", told)
	}
}

// The category a scan is scoped to travels through to the consumer, batch by
// batch -- todo 4.4's desired-item discovery needs it to know which policy
// (category.monitor_discovered) governs what it finds.
func TestJob_CarriesTheCategoryIDThroughToTheConsumer(t *testing.T) {
	fixture := newJobFixture(t, "Nick Cave/Tender Prey/01.flac")
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, CategoryID: 7, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, seen := range fixture.recorder.seenCategoryIDs {
		if seen != 7 {
			t.Errorf("consumer saw category %d, want 7", seen)
		}
	}
	if len(fixture.recorder.seenCategoryIDs) == 0 {
		t.Fatal("consumer was never called")
	}
}

// The second run is the point of the whole subsystem: at 500k files a rescan is not
// an answer.
func TestJob_SecondRunSeesOnlyWhatChanged(t *testing.T) {
	fixture := newJobFixture(t,
		"Nick Cave/Tender Prey/01.flac",
		"Bokuf Lipad/Rupe of Fofu/01.flac",
	)
	handler := fixture.job(t, scan.IsAudio)
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	fixture.recorder.seen = nil
	fixture.write(t, "Nick Cave/Tender Prey/02.flac")
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1}); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	// The whole changed directory comes back: the mtime says the listing moved,
	// not which entry.
	want := map[string]bool{
		"Nick Cave/Tender Prey/01.flac": true,
		"Nick Cave/Tender Prey/02.flac": true,
	}
	if len(fixture.recorder.seen) != len(want) {
		t.Fatalf("second run saw %v, want %v", fixture.recorder.paths(), want)
	}
	for _, relPath := range fixture.recorder.paths() {
		if !want[relPath] {
			t.Errorf("second run saw %q, which did not change", relPath)
		}
	}
}

// A scan across 500k files is a job with progress: what the consumer refused must
// come back, and what it accepted must not.
func TestJob_ResumesFromWhatTheConsumerCommitted(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac", "b/01.flac", "c/01.flac")
	handler := fixture.job(t, scan.IsAudio)

	refused := errors.New("write queue closed")
	fixture.recorder.failDirectory = "c"
	fixture.recorder.failure = refused
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); !errors.Is(err, refused) {
		t.Fatalf("Run = %v, want the consumer's error", err)
	}

	// The watermark must not have moved, or the resumed run would skip everything.
	progress := fixture.progress(t)
	if !progress.SweptThrough.IsZero() {
		t.Errorf("SweptThrough = %s after an interrupted scan", progress.SweptThrough)
	}
	if !progress.Running() {
		t.Error("an interrupted scan does not look interrupted, so nothing would resume it")
	}
	if progress.Cursor != "b" {
		t.Errorf("Cursor = %q, want the last directory committed", progress.Cursor)
	}

	fixture.recorder.failure = nil
	fixture.recorder.seen = nil
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if got := fixture.recorder.paths(); len(got) != 1 || got[0] != "c/01.flac" {
		t.Errorf("resumed run saw %v, want only what was left", got)
	}
	if fixture.progress(t).SweptThrough.IsZero() {
		t.Error("the resumed run did not complete")
	}
}

// An unmounted disk must not look like the loss of an entire library (section 10.4),
// so it retries rather than concluding anything.
func TestJob_RefusesToScanAnOfflineRootAndRetries(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac")
	fixture.roots[1] = settings.RootFolder{ID: 1, Path: fixture.root, Online: false}
	handler := fixture.job(t, scan.IsAudio)

	err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true})
	if err == nil {
		t.Fatal("scanning an offline root succeeded")
	}
	var classified *jobs.Error
	if !errors.As(err, &classified) || classified.Class != jobs.ClassRetryable {
		t.Errorf("Run = %v, want a retryable error: the disk may come back", err)
	}
	if len(fixture.recorder.seen) != 0 {
		t.Errorf("an offline root yielded %v", fixture.recorder.paths())
	}
}

// A root that is not configured will not become configured on a retry.
func TestJob_FailsTerminallyForAnUnknownRoot(t *testing.T) {
	fixture := newJobFixture(t)
	handler := fixture.job(t, scan.IsAudio)

	err := fixture.run(t, handler, scan.Payload{RootFolderID: 99})
	var classified *jobs.Error
	if !errors.As(err, &classified) || classified.Class != jobs.ClassTerminal {
		t.Errorf("Run = %v, want a terminal error for an unconfigured root", err)
	}
}

func TestJob_FailsTerminallyForAPayloadItCannotRead(t *testing.T) {
	fixture := newJobFixture(t)
	handler := fixture.job(t, scan.IsAudio)

	err := handler.Run(t.Context(), jobs.Job{Type: scan.JobType, Payload: []byte("{not json")})
	var classified *jobs.Error
	if !errors.As(err, &classified) || classified.Class != jobs.ClassTerminal {
		t.Errorf("Run = %v, want a terminal error: a bad payload will not parse next time", err)
	}
}

// One unreadable file is normal at 500k and must not stop the scan -- but it reaches
// the consumer carrying its error, so nothing downstream mistakes it for a file with
// no tags.
func TestJob_CarriesAnUnreadableFileThroughRatherThanStopping(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac", "a/broken.m4a")
	fixture.tool.failPath = path.Join(fixture.root, "a/broken.m4a")
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fixture.recorder.seen) != 2 {
		t.Fatalf("consumer saw %v, want both files", fixture.recorder.paths())
	}
	var withError int
	for _, observed := range fixture.recorder.seen {
		if observed.Probe.Err != nil {
			withError++
		}
	}
	if withError != 1 {
		t.Errorf("%d files carried a probe error, want exactly the broken one", withError)
	}
	if fixture.progress(t).SweptThrough.IsZero() {
		t.Error("one unreadable file stopped the scan from completing")
	}
}

// A media type decides what its own files look like (ADR-23); the sweep has no view.
func TestJob_ProbesOnlyWhatIncludeAccepts(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac", "a/cover.jpg", "a/notes.txt")
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := fixture.recorder.paths(); len(got) != 1 || got[0] != "a/01.flac" {
		t.Errorf("consumer saw %v, want only the audio", got)
	}
}

func TestIsAudio_AcceptsTheContainersStageThreeReads(t *testing.T) {
	for _, relPath := range []string{"a/01.FLAC", "a/01.mp3", "a/01.m4a", "a/01.Opus"} {
		if !scan.IsAudio(relPath) {
			t.Errorf("IsAudio(%q) = false", relPath)
		}
	}
	for _, relPath := range []string{"a/cover.jpg", "a/notes.txt", "a/01.flac.part", "a"} {
		if scan.IsAudio(relPath) {
			t.Errorf("IsAudio(%q) = true", relPath)
		}
	}
}

// The safety-critical behaviour of the whole feature (section 10.4): only a full
// sweep visited every directory, so only a full sweep is evidence that a path it
// never touched is actually gone.
func TestJob_FullSweepMarksStalePathsMissing(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac")
	// Planted directly, well before the sweep runs, at a location the walk never
	// visits -- the walk finishing without seeing it is exactly what should mark it.
	staleID := fixture.stalePath(t, "gone/vanished.flac", fixture.clock.Now().Unix())
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if missing := fixture.missingSince(t, staleID); !missing.Valid {
		t.Error("a path the full sweep never saw again was not marked missing")
	}
}

// An incremental sweep can skip whole directories whose mtime did not move, so a
// path it did not re-observe is not evidence of anything -- marking it missing here
// would be wrong, not merely unhelpful.
func TestJob_IncrementalSweepDoesNotMarkPathsMissing(t *testing.T) {
	fixture := newJobFixture(t, "a/01.flac")
	handler := fixture.job(t, scan.IsAudio)
	// A completed full sweep first, so the second run is a genuine incremental one.
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1, Full: true}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	staleID := fixture.stalePath(t, "gone/vanished.flac", fixture.clock.Now().Unix())
	if err := fixture.run(t, handler, scan.Payload{RootFolderID: 1}); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if missing := fixture.missingSince(t, staleID); missing.Valid {
		t.Error("an incremental sweep marked a path missing, but it never re-examined that directory")
	}
}

// At most one scan of a root may be active: two sweeps would fight over one cursor.
func TestActionKeyFor_IsPerRoot(t *testing.T) {
	if scan.ActionKeyFor(1) == scan.ActionKeyFor(2) {
		t.Error("two roots share an action key, so only one could ever be queued")
	}
	if !strings.Contains(scan.ActionKeyFor(7), "7") {
		t.Errorf("ActionKeyFor(7) = %q, want it to name the root", scan.ActionKeyFor(7))
	}
}

// A scoped rescan reads only the albums it names, nothing beneath them, and
// records no progress: it did not walk the root, so it is no watermark.
func TestJob_RescansOnlyTheNamedDirectoriesAndRecordsNoProgress(t *testing.T) {
	fixture := newJobFixture(t,
		"Heka/Detisisu Binip (1991)/01.m4a",
		"Heka/Detisisu Binip (1991)/02.m4a",
		"Heka/Detisisu Binip (1991)/scans/cover.m4a",
		"Heka/Zanuvode (1988)/01.m4a",
		"P!po/Kovalune (2023)/01.m4a",
	)
	handler := fixture.job(t, scan.IsAudio)

	if err := fixture.run(t, handler, scan.Payload{
		RootFolderID: 1, CategoryID: 3,
		Directories: []string{"Heka/Detisisu Binip (1991)", "P!po/Kovalune (2023)", "Gone/Deleted (2000)"},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := fixture.recorder.paths()
	slices.Sort(got)
	want := []string{
		"Heka/Detisisu Binip (1991)/01.m4a",
		"Heka/Detisisu Binip (1991)/02.m4a",
		"P!po/Kovalune (2023)/01.m4a",
	}
	if !slices.Equal(got, want) {
		t.Errorf("observed %v, want only the named directories' own files %v", got, want)
	}
	if progress := fixture.progress(t); progress != (library.ScanProgress{RootFolderID: 1}) {
		t.Errorf("progress = %+v, want none recorded by a scoped rescan", progress)
	}
}
