package importjob_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/importing/importjob"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
	"github.com/makearr/MediaNexus/migrations"

	"log/slog"
)

const root = "/downloads/incoming"
const releaseGroupMBID = "2a6bec3f-b635-464a-8072-f462e388ab05"

// fixedPolicy answers a constant commit threshold and never refuses anything
// by exclusion pattern -- the tests exercising ADR-53's own gate use their
// own fake (see poisoningPolicy below).
type fixedPolicy float64

func (p fixedPolicy) ImportAutoCommitConfidence() float64 { return float64(p) }

func (p fixedPolicy) RefusedByExclusion(string, string, *int64) (string, bool) { return "", false }

// pathTagTool answers ReadTags from a fixed table keyed by absolute path,
// carrying an error for any path listed in errPaths. failBatchOnce, when set,
// fails the whole call once for any batch containing that substring -- a
// transient failure that succeeds on retry, the shape a real network hiccup
// takes.
type pathTagTool struct {
	byPath        map[string]mediatool.Tags
	errPaths      map[string]bool
	failBatchOnce string

	// cancelAfterCalls, when non-zero, cancels cancelSourcePath's own session
	// the Nth time ReadTags runs -- a concurrent Cancel arriving mid-walk.
	cancelAfterCalls int
	cancelSourcePath string
	sessions         library.ImportSessions
	calls            int
	// onCall, when set, runs before every ReadTags call with the 1-based call
	// number -- a generic hook for injecting a concurrent action mid-walk
	// (todo 4.9's "assign a category later" test needs one between clusters).
	onCall func(callNumber int)
}

func (t *pathTagTool) Available(context.Context) error { return nil }

func (t *pathTagTool) ReadTags(ctx context.Context, paths []string) ([]mediatool.Probe, error) {
	t.calls++
	if t.cancelAfterCalls != 0 && t.calls == t.cancelAfterCalls {
		inspecting, _ := t.sessions.List(ctx, library.ImportInspecting)
		for _, session := range inspecting {
			if session.SourcePath == t.cancelSourcePath {
				_ = t.sessions.RequestCancel(ctx, session.ID)
			}
		}
	}
	if t.onCall != nil {
		t.onCall(t.calls)
	}
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
		Tool: f.tool, Packs: library.NewPacks(f.store), Clock: f.clock, Logger: slog.New(slog.DiscardHandler),
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

// poisoningPolicy refuses one exact file name -- ADR-53's own refuse action,
// stood in for a real settings.MatchExclusion lookup the way fixedPolicy
// stands in for a real commit threshold.
type poisoningPolicy struct {
	fixedPolicy
	refuseName string
}

func (p poisoningPolicy) RefusedByExclusion(name, _ string, _ *int64) (string, bool) {
	if name == p.refuseName {
		return "*.exe", true
	}
	return "", false
}

// ADR-53/todo 4.15: an executable beside real audio poisons the whole unit --
// even though "virus.exe" itself never classifies as media (it lands in
// inspect.Unit.Other, not Media), the unit's own two real tracks must still
// be refused, not committed with the executable quietly ignored. Confident
// embedded MBIDs on both tracks prove the override actually happens: without
// it, this exact fixture would otherwise clear the commit threshold (see
// TestJob_IdentifiesAndCommitsAConfidentUnit's own identical tags).
func TestJob_PoisonsAUnitCarryingARefusedFile(t *testing.T) {
	fx := newFixture(t, "Artist/Album/01.flac", "Artist/Album/02.flac", "Artist/Album/virus.exe")
	fx.tool.byPath[filepath.Join(root, "Artist/Album/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album/02.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := importjob.NewJob(importjob.Config{
		FS: fx.fsys, Sessions: library.NewImportSessions(fx.store), Policy: poisoningPolicy{refuseName: "virus.exe"},
		Tool: fx.tool, Packs: library.NewPacks(fx.store), Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
	})
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
	if list[0].UnitsCommitted != 0 {
		t.Errorf("UnitsCommitted = %d, want 0", list[0].UnitsCommitted)
	}

	units, err := sessions.ListUnits(t.Context(), list[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 1 || units[0].Decision != library.UnitSkip {
		t.Fatalf("units = %+v, want one SKIP", units)
	}
	if !strings.HasPrefix(units[0].Reason, "BLOCKED_PATTERN") {
		t.Errorf("Reason = %q, want a BLOCKED_PATTERN prefix", units[0].Reason)
	}
	if !strings.Contains(units[0].Reason, "virus.exe") {
		t.Errorf("Reason = %q, want it to name the refused file", units[0].Reason)
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

// A cancel between two clusters stops the walk at the next boundary and keeps whatever already committed.
func TestJob_StopsAtTheNextClusterBoundaryWhenCancelled(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.sessions = fx.sessions()
	fx.tool.cancelSourcePath = root
	fx.tool.cancelAfterCalls = 1 // requested while Album1's own probe call runs

	job := fx.job(t, 0.90)
	payload := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}
	if err := fx.run(t, job, payload); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cancelled, err := fx.sessions().List(t.Context(), library.ImportCancelled)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cancelled) != 1 {
		t.Fatalf("got %d CANCELLED sessions, want 1: %+v", len(cancelled), cancelled)
	}
	if cancelled[0].UnitsTotal != 1 {
		t.Errorf("UnitsTotal = %d, want 1 (Album1 committed before the cancel stopped Album2)",
			cancelled[0].UnitsTotal)
	}
	if fx.tool.calls != 1 {
		t.Errorf("ReadTags was called %d times, want 1 -- Album2 must never have been probed", fx.tool.calls)
	}
}

// A session left CANCELLING by a crash is finished as cancelled on the next Run, never resumed.
func TestJob_FinishesAnAlreadyCancellingSessionWithoutResuming(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac")
	sessions := fx.sessions()
	id, err := sessions.Create(t.Context(), library.NewSession{
		SourcePath: root, Origin: library.ImportOriginManual, StartedAt: fx.clock.Now().Unix(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.RequestCancel(t.Context(), id); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}

	job := fx.job(t, 0.90)
	payload := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}
	if err := fx.run(t, job, payload); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if fx.tool.calls != 0 {
		t.Errorf("ReadTags was called %d times, want 0 -- an already-cancelling session must not be walked", fx.tool.calls)
	}
	got, err := sessions.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.ImportCancelled {
		t.Errorf("State = %q, want %q", got.State, library.ImportCancelled)
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

// A fresh run's estimate is a plain, tag-free unit count -- no probes at all,
// since EstimateUnits never touches the tool.
func TestJob_RecordsAUnitsEstimate(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	done, err := fx.sessions().List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 || done[0].UnitsEstimated == nil || *done[0].UnitsEstimated != 2 {
		t.Fatalf("sessions = %+v, want one with UnitsEstimated=2", done)
	}
}

// The estimate is computed once, not recomputed on a resumed retry -- proven
// by adding a third album to the tree between the interrupted attempt and its
// retry: a recompute would see 3, the frozen estimate from before the retry
// still reads 2.
func TestJob_DoesNotRecomputeTheEstimateOnAResume(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.failBatchOnce = "Album2"

	job := fx.job(t, 0.90)
	payload := importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}
	if err := fx.run(t, job, payload); err == nil {
		t.Fatal("first Run succeeded, want the injected transient failure to have stopped it")
	}

	writeFile(t, fx.fsys, filepath.Join(root, "Artist/Album3/01.flac"), 200*1024)
	fx.tool.byPath[filepath.Join(root, "Artist/Album3/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	if err := fx.run(t, job, payload); err != nil {
		t.Fatalf("retried Run: %v", err)
	}

	done, err := fx.sessions().List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 || done[0].UnitsEstimated == nil || *done[0].UnitsEstimated != 2 {
		t.Fatalf("sessions = %+v, want the frozen estimate of 2, not a recompute seeing Album3", done)
	}
	if done[0].UnitsTotal != 3 {
		t.Errorf("UnitsTotal = %d, want 3 (Album3 was found and processed)", done[0].UnitsTotal)
	}
}

// Two flat "albums" dropped in the same directory with restarting track
// numbers split into two units by the track-continuity structural fallback
// (no tags feed GROUP itself, per Job.Run's own WalkOptions{}) -- one
// cluster, two units, each with its own confident, distinct MBID, exactly
// the shape identifyCluster's bounded fan-out must map back to the right
// ids. Both must independently reach COMMIT: AddUnits' own insert-time
// default is QUEUE at confidence 0, the same shape a *dropped* decision
// would leave behind, so anything less than "both really COMMIT" is a sign
// one unit's real result landed on the other's row, or was lost.
// Mutation-tested: writing every goroutine's result to decisions[0] instead
// of decisions[i] passes every other test in this file and is only caught here.
func TestJob_MapsEachUnitInAClusterToItsOwnDecision(t *testing.T) {
	const otherReleaseGroupMBID = "8c08f153-7820-4e0d-89ba-12c034eb022a"
	fx := newFixture(t)
	write := func(relPath string) {
		writeFile(t, fx.fsys, filepath.Join(root, relPath), 200*1024)
		fx.clock.Advance(time.Minute)
	}
	write("Messy/01 First.flac")
	write("Messy/02 Second.flac")
	write("Messy/01 Third.flac")
	write("Messy/02 Fourth.flac")
	fx.tool.byPath[filepath.Join(root, "Messy/01 First.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Messy/01 Third.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": otherReleaseGroupMBID}

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	done, err := fx.sessions().List(t.Context(), library.ImportDone)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(done) != 1 || done[0].UnitsTotal != 2 || done[0].UnitsCommitted != 2 {
		t.Fatalf("sessions = %+v, want one session with 2 units, both committed", done)
	}
	units, err := fx.sessions().ListUnits(t.Context(), done[0].ID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("units = %+v, want 2", units)
	}
	for _, u := range units {
		if u.Decision != library.UnitCommit || u.Confidence == 0 {
			t.Errorf("unit %+v, want COMMIT with a real confidence -- QUEUE/0 is what a dropped decision looks like", u)
		}
	}
}

// commitCluster reads the session's own category fresh every cluster rather
// than trusting the job's frozen enqueue-time payload (job.go's own doc
// comment on commitCluster) -- todo 4.9's "assign a category later" UI
// action can set import_session.category_id while a session is still
// INSPECTING, and a cluster committed after that must place into it. Two
// separate top-level directories make two separate clusters
// (TestJob_StopsAtTheNextClusterBoundaryWhenCancelled already relies on the
// same split); the category is set between them, during the first cluster's
// own probe.
func TestJob_PicksUpACategoryAssignedMidRun(t *testing.T) {
	fx := newFixture(t, "Artist/Album1/01.flac", "Artist/Album2/01.flac")
	fx.tool.byPath[filepath.Join(root, "Artist/Album1/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}
	fx.tool.byPath[filepath.Join(root, "Artist/Album2/01.flac")] =
		mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}

	sessions := fx.sessions()
	identity := library.NewIdentity(fx.store)
	resolutions, err := identity.ResolveWorksDetailed(t.Context(), fx.clock.Now(), []library.WorkLink{{
		Provider: "test", ExternalID: t.Name(),
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: t.Name(), SortTitle: t.Name()},
	}})
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	committer := &fakeCommitter{workID: resolutions[0].WorkID}

	fx.tool.onCall = func(callNumber int) {
		if callNumber != 1 {
			return
		}
		inspecting, err := sessions.List(t.Context(), library.ImportInspecting)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, s := range inspecting {
			if s.SourcePath == root {
				if err := sessions.SetCategory(t.Context(), s.ID, 9); err != nil {
					t.Fatalf("SetCategory: %v", err)
				}
			}
		}
	}

	job := importjob.NewJob(importjob.Config{
		FS: fx.fsys, Sessions: sessions, Policy: fixedPolicy(0.90),
		Tool: fx.tool, Committer: committer, Packs: library.NewPacks(fx.store), Clock: fx.clock, Logger: slog.New(slog.DiscardHandler),
	})
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	done, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil || len(done) != 1 {
		t.Fatalf("List: %v, %+v", err, done)
	}
	units, err := sessions.ListUnits(t.Context(), done[0].ID)
	if err != nil || len(units) != 2 {
		t.Fatalf("ListUnits: %v, %+v", err, units)
	}
	for _, u := range units {
		switch u.RelPath {
		case "Artist/Album1":
			if u.WorkID != nil {
				t.Errorf("Album1 WorkID = %v, want nil -- its own cluster ran before the category was set", u.WorkID)
			}
		case "Artist/Album2":
			if u.WorkID == nil {
				t.Error("Album2 WorkID is nil, want placed -- its own cluster ran after the category was set")
			}
		default:
			t.Errorf("unexpected unit rel_path %q", u.RelPath)
		}
	}
	if len(committer.calls) != 1 {
		t.Fatalf("committer was called %d times, want 1 (Album2 only)", len(committer.calls))
	}
	if committer.calls[0].directory != "Artist/Album2" {
		t.Errorf("committer was called for %q, want Artist/Album2", committer.calls[0].directory)
	}
}

// Two releases by the same artist, each its own top-level directory under
// the walked source (the real scene-fixture shape /storage/test/medianexus/
// music/ itself has for this exact artist) -- todo 4.11's own detector
// groups them into one pack once the whole walk finishes, whatever each
// unit's own decision turned out to be (both QUEUE here, no embedded MBID).
func TestJob_DetectsAPackFromSiblingArtistAgreement(t *testing.T) {
	fx := newFixture(t,
		"Lamig Vu Linumudo-Cedak Tivilute-CDM-FLAC-1996-CHS/01.flac",
		"Lamig Vu Linumudo-The Mukaminu Rerer-SINGLE-16BIT-WEB-FLAC-2018-FLACED/01.flac",
		"Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES/01.flac",
	)

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginManual}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	sessions := fx.sessions()
	done, err := sessions.List(t.Context(), library.ImportDone)
	if err != nil || len(done) != 1 {
		t.Fatalf("List: %v, %+v", err, done)
	}
	units, err := sessions.ListUnits(t.Context(), done[0].ID)
	if err != nil || len(units) != 3 {
		t.Fatalf("ListUnits: %v, %+v", err, units)
	}

	packs := library.NewPacks(fx.store)
	page, err := packs.ListPage(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("packs = %+v, want exactly 1 (Weird Al only, not Vutu Doci)", page)
	}
	if page[0].Kind != library.PackDiscography || page[0].MemberCount != 2 {
		t.Errorf("pack = %+v, want DISCOGRAPHY with 2 members", page[0])
	}

	var kateBushUnit library.Unit
	packedCount := 0
	for _, u := range units {
		if u.PackID != nil {
			packedCount++
			if *u.PackID != page[0].ID {
				t.Errorf("unit %d PackID = %d, want %d", u.ID, *u.PackID, page[0].ID)
			}
		} else if strings.Contains(u.RelPath, "Vutu Doci") {
			kateBushUnit = u
		}
	}
	if packedCount != 2 {
		t.Errorf("packedCount = %d, want 2", packedCount)
	}
	if kateBushUnit.ID == 0 {
		t.Error("Vutu Doci's own unit was not found unpacked")
	}
}

// An adoption session (an existing, already-organised library) never
// triggers pack detection, even when its own units would otherwise agree by
// artist -- an established catalogue is not "one source, many works"
// (private/questions.md).
func TestJob_SkipsPackDetectionForAdoption(t *testing.T) {
	fx := newFixture(t,
		"Lamig Vu Linumudo-Cedak Tivilute-CDM-FLAC-1996-CHS/01.flac",
		"Lamig Vu Linumudo-The Mukaminu Rerer-SINGLE-16BIT-WEB-FLAC-2018-FLACED/01.flac",
	)

	job := fx.job(t, 0.90)
	if err := fx.run(t, job, importjob.Payload{SourcePath: root, Origin: library.ImportOriginAdoption}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	page, err := library.NewPacks(fx.store).ListPage(t.Context(), 0, 10)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page) != 0 {
		t.Errorf("packs = %+v, want none for an adoption session", page)
	}
}

func TestActionKeyFor_IsPerSourcePath(t *testing.T) {
	if importjob.ActionKeyFor("/a") == importjob.ActionKeyFor("/b") {
		t.Error("ActionKeyFor gave the same key for two different source paths")
	}
}
