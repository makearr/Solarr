package library_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
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

	ids, err := sessions.AddUnits(ctx, id, []library.NewUnit{
		{RelPath: "Artist A/Album 1", Shape: library.UnitSet, FileCount: 10, Bytes: 1000, EvidenceJSON: "{}"},
		{RelPath: "Artist B/Album 2", Shape: library.UnitSet, FileCount: 8, Bytes: 800, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	if len(ids) != 2 || ids[0] == 0 || ids[1] == 0 || ids[0] == ids[1] {
		t.Errorf("AddUnits ids = %v, want 2 distinct non-zero ids", ids)
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

func TestImportSessions_ListUnitsPagePaginatesAndFiltersByDecision(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ids, err := sessions.AddUnits(ctx, id, []library.NewUnit{
		{RelPath: "Album 1", Shape: library.UnitSet, FileCount: 1, Bytes: 100, EvidenceJSON: "{}"},
		{RelPath: "Album 2", Shape: library.UnitSet, FileCount: 1, Bytes: 100, EvidenceJSON: "{}"},
		{RelPath: "Album 3", Shape: library.UnitSet, FileCount: 1, Bytes: 100, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	created := createWork(t, opened, "music", "Album 1")
	if err := sessions.SetUnitDecisions(ctx, id, []library.UnitDecision{
		{UnitID: ids[0], Decision: library.UnitCommit, Confidence: 0.96, WorkID: &created.ID},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}

	page1, err := sessions.ListUnitsPage(ctx, id, 0, "", 2)
	if err != nil {
		t.Fatalf("ListUnitsPage: %v", err)
	}
	if len(page1) != 2 || page1[0].RelPath != "Album 1" || page1[1].RelPath != "Album 2" {
		t.Fatalf("page1 = %+v, want Album 1 then Album 2", page1)
	}

	page2, err := sessions.ListUnitsPage(ctx, id, page1[len(page1)-1].ID, "", 2)
	if err != nil {
		t.Fatalf("ListUnitsPage: %v", err)
	}
	if len(page2) != 1 || page2[0].RelPath != "Album 3" {
		t.Fatalf("page2 = %+v, want just Album 3", page2)
	}

	committed, err := sessions.ListUnitsPage(ctx, id, 0, library.UnitCommit, 10)
	if err != nil {
		t.Fatalf("ListUnitsPage filtered: %v", err)
	}
	if len(committed) != 1 || committed[0].RelPath != "Album 1" {
		t.Fatalf("committed = %+v, want only Album 1", committed)
	}
}

func TestImportSessions_SetUnitDecisionsRecomputesCounts(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/messy", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := sessions.AddUnits(ctx, id, []library.NewUnit{
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
	if err := sessions.SetUnitDecisions(ctx, id, []library.UnitDecision{
		{UnitID: units[0].ID, Decision: library.UnitCommit, Confidence: 0.96, WorkID: &work},
	}); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
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

func TestImportSessions_RequestCancelOnlyFromInspecting(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := sessions.RequestCancel(ctx, id); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}
	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != library.ImportCancelling {
		t.Errorf("State = %q, want %q", got.State, library.ImportCancelling)
	}

	// Already CANCELLING: a second request is refused, not a silent no-op.
	if err := sessions.RequestCancel(ctx, id); !errors.Is(err, library.ErrSessionNotCancellable) {
		t.Errorf("second RequestCancel = %v, want ErrSessionNotCancellable", err)
	}
}

func TestImportSessions_RequestCancelRefusesATerminalSession(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.Finish(ctx, id, library.ImportDone, 200, ""); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if err := sessions.RequestCancel(ctx, id); !errors.Is(err, library.ErrSessionNotCancellable) {
		t.Errorf("RequestCancel on a DONE session = %v, want ErrSessionNotCancellable", err)
	}
}

func TestImportSessions_RecomputeCountsIncludesSkippedAndFailed(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ids, err := sessions.AddUnits(ctx, id, []library.NewUnit{
		{RelPath: "commit", Shape: library.UnitSingle, FileCount: 1, Bytes: 10, EvidenceJSON: "{}"},
		{RelPath: "skip", Shape: library.UnitSingle, FileCount: 1, Bytes: 20, EvidenceJSON: "{}"},
		{RelPath: "failed", Shape: library.UnitSingle, FileCount: 1, Bytes: 30, EvidenceJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	decisions := make([]library.UnitDecision, 3)
	for i, decision := range []string{library.UnitCommit, library.UnitSkip, library.UnitFailed} {
		decisions[i] = library.UnitDecision{UnitID: ids[i], Decision: decision, Confidence: 0.5}
	}
	if err := sessions.SetUnitDecisions(ctx, id, decisions); err != nil {
		t.Fatalf("SetUnitDecisions: %v", err)
	}

	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UnitsCommitted != 1 || got.UnitsSkipped != 1 || got.UnitsFailed != 1 || got.UnitsTotal != 3 {
		t.Errorf("counts = total %d, committed %d, skipped %d, failed %d, want 3/1/1/1",
			got.UnitsTotal, got.UnitsCommitted, got.UnitsSkipped, got.UnitsFailed)
	}
}

func TestImportSessions_SetCategoryFromNone(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.SetCategory(ctx, id, 7); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	got, err := sessions.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CategoryID == nil || *got.CategoryID != 7 {
		t.Errorf("CategoryID = %v, want 7", got.CategoryID)
	}
}

// Calling SetCategory again with the *same* category is deliberately not an
// error -- it is how a caller retries ManualCommitter.PlaceUnplaced after a
// transient placement failure (notes.md: a root folder's capacity not probed
// yet is exactly this shape, found live-verifying todo 4.9's UI half).
func TestImportSessions_SetCategoryIsIdempotentForTheSameCategory(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.SetCategory(ctx, id, 7); err != nil {
		t.Fatalf("first SetCategory: %v", err)
	}
	if err := sessions.SetCategory(ctx, id, 7); err != nil {
		t.Errorf("second SetCategory with the same category = %v, want nil", err)
	}
}

func TestImportSessions_SetCategoryRefusesADifferentCategory(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	ctx := t.Context()

	id, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/a", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessions.SetCategory(ctx, id, 7); err != nil {
		t.Fatalf("first SetCategory: %v", err)
	}
	err = sessions.SetCategory(ctx, id, 8)
	if !errors.Is(err, library.ErrSessionCategoryAlreadySet) {
		t.Errorf("err = %v, want ErrSessionCategoryAlreadySet", err)
	}
	got, getErr := sessions.Get(ctx, id)
	if getErr != nil {
		t.Fatalf("Get: %v", getErr)
	}
	if got.CategoryID == nil || *got.CategoryID != 7 {
		t.Errorf("CategoryID = %v, want unchanged at 7", got.CategoryID)
	}
}
