package importjob

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
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
