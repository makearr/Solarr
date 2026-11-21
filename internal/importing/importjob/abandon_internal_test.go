package importjob

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func abandonFixture(t *testing.T) (*Job, library.ImportSessions, int64, []int64) {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o750); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{Name: "library", Path: filepath.Join(directory, "library.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatal(err)
	}
	sessions := library.NewImportSessions(opened)
	sessionID, err := sessions.Create(t.Context(), library.NewSession{SourcePath: "/dl", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	ids, err := sessions.AddUnits(t.Context(), sessionID, []library.NewUnit{
		{RelPath: "a", Shape: library.UnitSet, FileCount: 1, EvidenceJSON: "{}"}, {RelPath: "b", Shape: library.UnitSet, FileCount: 1, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &Job{sessions: sessions, logger: slog.New(slog.DiscardHandler)}, sessions, sessionID, ids
}

// A cluster nothing of which was recorded is forgotten whole, to be inspected again; once an earlier group is recorded
// the units of the failed one are held, because a retry would walk past them (IMPJ-3).
func TestAbandonUnrecorded_ForgetsAnUntouchedClusterAndHoldsTheRestOfAPartlyRecordedOne(t *testing.T) {
	job, sessions, sessionID, ids := abandonFixture(t)
	failed := []importGroup{{members: []int{1}}}
	job.abandonUnrecorded(t.Context(), sessionID, ids, failed, true, errors.New("database is locked"))
	units, err := sessions.ListUnits(t.Context(), sessionID)
	if err != nil || len(units) != 2 || units[0].Reason != "" ||
		units[1].Reason != "the import stopped before this unit: database is locked" {
		t.Fatalf("units = %+v, %v; want unit b held with the cause and a left alone", units, err)
	}
	job.abandonUnrecorded(t.Context(), sessionID, ids, failed, false, errors.New("database is locked"))
	if units, err := sessions.ListUnits(t.Context(), sessionID); err != nil || len(units) != 0 {
		t.Fatalf("units = %+v, %v; want the untouched cluster forgotten", units, err)
	}
}

type typedImporter struct {
	extension string
	err       error
}

func (i typedImporter) Fit(unit inspect.Unit) float64 {
	if path.Ext(unit.Media[0].RelPath) == i.extension {
		return 0.9
	}
	return 0
}

func (i typedImporter) ImportBatch(_ context.Context, request importbatch.Request) (importbatch.Result, error) {
	if i.err != nil {
		return importbatch.Result{}, i.err
	}
	result := importbatch.Result{Placements: make([]*library.Placement, len(request.Units))}
	for k := range request.Units {
		result.Decisions = append(result.Decisions, library.UnitDecision{UnitID: request.UnitIDs[k], Decision: library.UnitCommit})
	}
	return result, nil
}

type openPolicy struct{}

func (openPolicy) ImportAutoCommitConfidence() float64                      { return 0.9 }
func (openPolicy) RefusedByExclusion(string, string, *int64) (string, bool) { return "", false }

// A cluster of two media types whose second import fails keeps the first type's recorded unit and holds the failed
// type's unit, instead of forgetting the whole cluster or leaving the unit undecided (IMPJ-3).
func TestCommitCluster_ALaterTypesFailureKeepsTheEarlierUnitAndHoldsItsOwn(t *testing.T) {
	job, sessions, sessionID, _ := abandonFixture(t)
	if units, err := sessions.ListUnits(t.Context(), sessionID); err != nil || len(units) != 2 {
		t.Fatalf("units = %+v, %v", units, err)
	}
	if err := sessions.DeleteUnits(t.Context(), sessionID, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	job.importers = map[string]importbatch.Importer{
		"music": typedImporter{extension: ".flac"}, "tv": typedImporter{extension: ".mkv", err: errors.New("database is locked")},
	}
	job.policy = openPolicy{}
	job.libraries = func() []Library {
		return []Library{{CategoryID: 1, MediaType: "music", General: true}, {CategoryID: 2, MediaType: "tv", General: true}}
	}
	batch := []inspect.Unit{
		{Directory: "Album", Media: []inspect.File{{RelPath: "Album/01.flac", SizeBytes: 5 << 20}}},
		{Directory: "Show", Media: []inspect.File{{RelPath: "Show/e01.mkv", SizeBytes: 5 << 20}}},
	}
	err := job.commitCluster(t.Context(), sessionID, "/dl", 0.9, false, false, true, nil, batch)
	if err == nil {
		t.Fatal("commitCluster succeeded, want the tv batch's failure")
	}
	units, err := sessions.ListUnits(t.Context(), sessionID)
	if err != nil || len(units) != 2 {
		t.Fatalf("units = %+v, %v; want both kept", units, err)
	}
	got := map[string][2]string{}
	for _, unit := range units {
		got[unit.RelPath] = [2]string{unit.Decision, unit.Reason}
	}
	want := map[string][2]string{
		"Album": {library.UnitCommit, ""},
		"Show":  {library.UnitQueue, "the import stopped before this unit: database is locked"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("units = %v, want %v", got, want)
	}
}
