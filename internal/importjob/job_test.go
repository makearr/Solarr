package importjob_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/importjob"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/migrations"

	"log/slog"
)

const root = "/downloads/incoming"
const releaseGroupMBID = "2a6bec3f-b635-464a-8072-f462e388ab05"

// fixedPolicy answers a constant commit threshold.
type fixedPolicy float64

func (p fixedPolicy) ImportAutoCommitConfidence() float64 { return float64(p) }

// pathTagTool answers ReadTags from a fixed table keyed by absolute path,
// carrying an error for any path listed in errPaths. failBatchOnce, when set,
// fails the whole call once for any batch containing that substring -- a
// transient failure that succeeds on retry, the shape a real network hiccup
// takes.
type pathTagTool struct {
	byPath        map[string]mediatool.Tags
	errPaths      map[string]bool
	failBatchOnce string
}

func (t *pathTagTool) Available(context.Context) error { return nil }

func (t *pathTagTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	if t.failBatchOnce != "" {
		for _, p := range paths {
			if strings.Contains(p, t.failBatchOnce) {
				t.failBatchOnce = ""
				return nil, errors.New("transient probe failure")
			}
		}
	}
	probes := make([]mediatool.Probe, len(paths))
	for i, p := range paths {
		if t.errPaths[p] {
			probes[i] = mediatool.Probe{Path: p, Err: errors.New("moov atom not found")}
			continue
		}
		probes[i] = mediatool.Probe{Path: p, Tags: t.byPath[p]}
	}
	return probes, nil
}

func (t *pathTagTool) WriteTags(context.Context, []mediatool.TagEdit) error {
	return mediatool.ErrWriteUnsupported
}

func (t *pathTagTool) Fingerprint(context.Context, []string) ([]mediatool.Fingerprint, error) {
	return nil, nil
}

func writeFile(t *testing.T, fsys *fsx.Mem, absolute string, sizeBytes int) {
	t.Helper()
	if err := fsys.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	f, err := fsys.Create(absolute)
	if err != nil {
		t.Fatalf("Create(%q): %v", absolute, err)
	}
	if _, err := f.Write(make([]byte, sizeBytes)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

type fixture struct {
	fsys  *fsx.Mem
	store *store.Store
	clock *clock.Fake
	tool  *pathTagTool
}

func newFixture(t *testing.T, files ...string) *fixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	memory := fsx.NewMem(fake)
	if err := memory.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", root, err)
	}
	for _, relPath := range files {
		writeFile(t, memory, filepath.Join(root, relPath), 200*1024)
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

	return &fixture{
		fsys: memory, store: opened, clock: fake,
		tool: &pathTagTool{byPath: map[string]mediatool.Tags{}, errPaths: map[string]bool{}},
	}
}

func (f *fixture) job(t *testing.T, policy float64) *importjob.Job {
	t.Helper()
	return importjob.NewJob(importjob.Config{
		FS: f.fsys, Sessions: library.NewImportSessions(f.store), Policy: fixedPolicy(policy),
		Tool: f.tool, Clock: f.clock, Logger: slog.New(slog.DiscardHandler),
	})
}

func (f *fixture) run(t *testing.T, job *importjob.Job, payload importjob.Payload) error {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return job.Run(t.Context(), jobs.Job{Type: importjob.JobType, Payload: data})
}

func (f *fixture) sessions() library.ImportSessions { return library.NewImportSessions(f.store) }

// A unit with two files agreeing on a real embedded release-group MBID clears
// the default commit threshold (0.90 < confidenceReleaseGroup 0.95) and
// records COMMIT; the session reaches ImportDone.
func TestJob_IdentifiesAndCommitsAConfidentUnit(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := fx.sessions()
	list, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d done sessions, want 1", len(list))
	}
	if list[0].UnitsCommitted != 1 || list[0].UnitsTotal != 1 {
		t.Errorf("counts = total=%d committed=%d, want 1/1", list[0].UnitsTotal, list[0].UnitsCommitted)
	}

	units, err := sessions.ListUnits(t.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 1 || units[0].Decision != library.UnitCommit {
		t.Fatalf("units = %+v, want one COMMIT", units)
	}
	if units[0].Shape != library.UnitSet {
		t.Errorf("Shape = %q, want %q", units[0].Shape, library.UnitSet)
	}
}

// No embedded identity at all: the unit queues rather than guessing.
func TestJob_QueuesAnUnidentifiedUnit(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac")

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := fx.sessions()
	list, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	units, err := sessions.ListUnits(t.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 1 || units[0].Decision != library.UnitQueue {
		t.Fatalf("units = %+v, want one QUEUE", units)
	}
}

// Every file in the unit unreadable: recorded as FAILED, not left silently at
// the QUEUE default and not propagated as a job failure -- one bad unit must
// not stop the rest of a resumable walk.
func TestJob_RecordsFailedForAnUnreadableUnit(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac")
	fx.tool.errPaths[filepath.Join(root, "Artist/Album/01.flac")] = true

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := fx.sessions()
	list, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	units, err := sessions.ListUnits(t.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 1 || units[0].Decision != library.UnitFailed {
		t.Fatalf("units = %+v, want one FAILED", units)
	}
}

// The exact scenario resumeOrCreate exists for: a job's own executor retries
// on a plain error, and a retry of the same payload must resume the session
// an interrupted first attempt already started, not duplicate it or its
// already-committed units.
func TestJob_ResumesRatherThanDuplicatingOnARetry(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	// Album2 is lexically after Album1, so this fails the second cluster's
	// probe on the first attempt only -- a transient failure, not a bad file.
	fx.tool.failBatchOnce = "Album2"

	job := fx.job(t, 0.90)
	payload := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}

	if err := fx.run(t, job, payload); err == nil {
		t.Fatal("first Run succeeded, want the injected transient failure to have stopped it")
	}
	sessions := fx.sessions()
	inspecting, err := sessions.List(t.Context(), library.ImportInspecting)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inspecting) != 1 || inspecting[0].UnitsTotal != 1 {
		t.Fatalf("after the interrupted run, sessions = %+v, want 1 INSPECTING session with 1 unit so far",
			inspecting)
	}

	if err := fx.run(t, job, payload); err != nil {
		t.Fatalf("retried Run: %v", err)
	}

	done, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 {
		t.Fatalf("got %d done sessions after the retry, want 1 (the same one resumed): %+v", len(done), done)
	}
	if done[0].ID != inspecting[0].ID {
		t.Errorf("the retry created a new session (%d) instead of resuming the interrupted one (%d)",
			done[0].ID, inspecting[0].ID)
	}
	units, err := sessions.ListUnits(t.Context(), done[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units after the retry, want 2 (no duplicate of Album1): %+v", len(units), units)
	}
}

// resumeOrCreate must match on source path, not just "any session in
// progress" -- two different imports interrupted at the same time must each
// resume their own session, never each other's.
func TestJob_ResumesTheMatchingSessionWhenSeveralAreInProgress(t *testing.T) {
	const rootB = "/downloads/other"
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	writeFile(t, fx.fsys, rootB+"/Band/Record/01.flac", 200*1024)
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(rootB, "Band/Record/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90)
	payloadA := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}
	payloadB := importjob.Payload{SourcePath: rootB, Origin: library.ImportOriginManual}

	fx.tool.failBatchOnce = "Album2"
	if err := fx.run(t, job, payloadA); err == nil {
		t.Fatal("Run for A succeeded, want the injected failure to have stopped it")
	}
	if err := fx.run(t, job, payloadB); err != nil {
		t.Fatalf("Run for B: %v", err)
	}

	sessions := fx.sessions()
	inspecting, err := sessions.List(t.Context(), library.ImportInspecting)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inspecting) != 1 || inspecting[0].SourcePath != root {
		t.Fatalf("in-progress sessions = %+v, want exactly A still INSPECTING", inspecting)
	}

	if err := fx.run(t, job, payloadA); err != nil {
		t.Fatalf("retried Run for A: %v", err)
	}

	done, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 2 {
		t.Fatalf("got %d done sessions, want 2 (A resumed, B untouched): %+v", len(done), done)
	}
	for _, s := range done {
		if s.SourcePath == root && s.UnitsTotal != 2 {
			t.Errorf("A's resumed session has %d units, want 2 (no duplicate of Album1)", s.UnitsTotal)
		}
		if s.SourcePath == rootB && s.UnitsTotal != 1 {
			t.Errorf("B's session has %d units, want 1, untouched by A's retry", s.UnitsTotal)
		}
	}
}

func TestJob_FailsTerminallyForAPayloadItCannotRead(t *testing.T) {
	fx := newFixture(t)
	job := fx.job(t, 0.90)

	err := job.Run(t.Context(), jobs.Job{Type: importjob.JobType, Payload: []byte("{not json")})
	var classified *jobs.Error
	if !errors.As(err, &classified) || classified.Class != jobs.ClassTerminal {
		t.Errorf("Run = %v, want a terminal error: a bad payload will not parse next time", err)
	}
}

func TestActionKeyFor_IsPerSourcePath(t *testing.T) {
	if importjob.ActionKeyFor("/a") == importjob.ActionKeyFor("/b") {
		t.Error("ActionKeyFor gave the same key for two different source paths")
	}
}
