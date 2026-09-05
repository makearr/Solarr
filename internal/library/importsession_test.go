package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/library"
)

func TestImportSessions_CreateAndGet(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual,
		Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.ImportInspecting {
		t.Errorf("State = %q, want %q", got.State, library.ImportInspecting)
	}
	if got.SourcePath != "/downloads/messy" || got.Origin != library.ImportOriginManual {
		t.Errorf("SourcePath/Origin = %q/%q, want /downloads/messy/manual", got.SourcePath, got.Origin)
	}
	if got.UnitsTotal != 0 || got.UnitsCommitted != 0 || got.UnitsQueued != 0 {
		t.Errorf("counts = %d/%d/%d, want all zero", got.UnitsTotal, got.UnitsCommitted, got.UnitsQueued)
	}
	if got.AcquisitionID != nil || got.CategoryID != nil || got.FinishedAt != nil {
		t.Errorf("optional fields should be nil on a fresh session: %+v", got)
	}
}

func TestImportSessions_AddUnitsRecomputesCounts(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = sessions.AddUnits(ctx, id, []library.NewUnit{
		{RelPath: "Artist A/Album 1", Shape: library.UnitSet, FileCount: 10, Bytes: 1000, EvidenceJSON: "{}"},
		{RelPath: "Artist B/Album 2", Shape: library.UnitSet, FileCount: 8, Bytes: 800, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}

	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UnitsTotal != 2 || got.UnitsQueued != 2 || got.UnitsCommitted != 0 {
		t.Errorf("counts = total=%d queued=%d committed=%d, want 2/2/0",
			got.UnitsTotal, got.UnitsQueued, got.UnitsCommitted)
	}
	if got.BytesImported != 0 {
		t.Errorf("BytesImported = %d, want 0 -- nothing has committed yet", got.BytesImported)
	}

	units, err := sessions.ListUnits(ctx, id)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("len(units) = %d, want 2", len(units))
	}
	for _, u := range units {
		if u.Decision != library.UnitQueue {
			t.Errorf("unit %d decision = %q, want %q (the default before IDENTIFY exists)",
				u.ID, u.Decision, library.UnitQueue)
		}
		if u.WorkID != nil || u.EditionID != nil {
			t.Errorf("unit %d should have no identity yet: %+v", u.ID, u)
		}
	}
}

func TestImportSessions_SetUnitDecisionRecomputesCounts(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.AddUnits(ctx, id, []library.NewUnit{
		{RelPath: "Artist A/Album 1", Shape: library.UnitSet, FileCount: 10, Bytes: 1000, EvidenceJSON: "{}"},
		{RelPath: "Artist B/Album 2", Shape: library.UnitSet, FileCount: 8, Bytes: 800, EvidenceJSON: "{}"},
	}); err != nil {
		t.Fatalf("AddUnits: %v", err)
	}

	units, err := sessions.ListUnits(ctx, id)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	// import_unit.work_id has a real FK to work(id) -- a fake id would fail it.
	created := createWork(t, opened, "music", "Album 1")
	work := created.ID
	if err := sessions.SetUnitDecision(ctx, units[0].ID, library.UnitCommit, 0.96, &work, nil, "", ""); err != nil {
		t.Fatalf("SetUnitDecision: %v", err)
	}

	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UnitsCommitted != 1 || got.UnitsQueued != 1 {
		t.Errorf("committed/queued = %d/%d, want 1/1", got.UnitsCommitted, got.UnitsQueued)
	}
	if got.BytesImported != 1000 {
		t.Errorf("BytesImported = %d, want 1000 (only the committed unit's bytes)", got.BytesImported)
	}

	committed, err := sessions.ListUnits(ctx, id)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	if committed[0].Decision != library.UnitCommit || committed[0].WorkID == nil || *committed[0].WorkID != work {
		t.Errorf("unit 0 = %+v, want Decision=COMMIT WorkID=%d", committed[0], work)
	}
}

func TestImportSessions_Finish(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := sessions.Finish(ctx, id, library.ImportFailed, 200, "disk full"); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.ImportFailed {
		t.Errorf("State = %q, want %q", got.State, library.ImportFailed)
	}
	if got.FinishedAt == nil || *got.FinishedAt != 200 {
		t.Errorf("FinishedAt = %v, want 200", got.FinishedAt)
	}
	if got.Error != "disk full" {
		t.Errorf("Error = %q, want %q", got.Error, "disk full")
	}
}

func TestImportSessions_ListFiltersByState(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	inspecting, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	failed, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/b", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 200,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.Finish(ctx, failed, library.ImportFailed, 300, "boom"); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	all, err := sessions.List(ctx, "")
	if err != nil {
		t.Fatalf("List(\"\"): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}
	// Newest first (started_at DESC).
	if all[0].ID != failed || all[1].ID != inspecting {
		t.Errorf("order = [%d, %d], want [%d, %d] (newest first)", all[0].ID, all[1].ID, failed, inspecting)
	}

	onlyFailed, err := sessions.List(ctx, library.ImportFailed)
	if err != nil {
		t.Fatalf("List(FAILED): %v", err)
	}
	if len(onlyFailed) != 1 || onlyFailed[0].ID != failed {
		t.Errorf("List(FAILED) = %+v, want just session %d", onlyFailed, failed)
	}
}
