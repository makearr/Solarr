package library_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
)

// seedSessionWithUnits creates one session and n undecided units, answering
// the session id and each unit's own id in insertion order -- pack_test.go's
// own fixture, since a pack always references real import_unit rows (the FK
// pack_item.import_unit_id and import_unit.pack_id both carry).
func seedSessionWithUnits(t *testing.T, sessions library.ImportSessions, n int) (sessionID int64, unitIDs []int64) {
	t.Helper()
	ctx := t.Context()
	sessionID, err := sessions.Create(ctx, library.NewSession{
		SourcePath: "/downloads/x", Origin: library.ImportOriginManual, Threshold: 0.9, StartedAt: 100,
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	units := make([]library.NewUnit, n)
	for i := range units {
		units[i] = library.NewUnit{RelPath: "Album", Shape: library.UnitSet, FileCount: 1, Bytes: 10, EvidenceJSON: "{}"}
	}
	unitIDs, err = sessions.AddUnits(ctx, sessionID, units)
	if err != nil {
		t.Fatalf("AddUnits: %v", err)
	}
	return sessionID, unitIDs
}

func TestPacks_CreateStampsPackIDOntoEveryMember(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	packs := library.NewPacks(opened)
	sessionID, unitIDs := seedSessionWithUnits(t, sessions, 3)

	items := make([]library.NewPackItem, len(unitIDs))
	for i, id := range unitIDs {
		items[i] = library.NewPackItem{ImportUnitID: id, State: library.PackItemQueued}
	}
	packID, err := packs.Create(t.Context(), library.PackDiscography, library.PackDetectedFromStructureAndTitle,
		0.85, "XT+QK", sessionID, 200, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := packs.Get(t.Context(), packID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != library.PackDiscography || got.Title != "XT+QK" || got.MemberCount != 3 || got.ResolvedCount != 0 {
		t.Errorf("pack = %+v, want DISCOGRAPHY/XT+QK/3/0", got)
	}
	if got.SessionID == nil || *got.SessionID != sessionID {
		t.Errorf("SessionID = %v, want %d", got.SessionID, sessionID)
	}

	packItems, err := packs.Items(t.Context(), packID)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(packItems) != 3 {
		t.Fatalf("Items = %+v, want 3", packItems)
	}
	for i, item := range packItems {
		if item.ImportUnitID == nil || *item.ImportUnitID != unitIDs[i] || item.State != library.PackItemQueued {
			t.Errorf("item %d = %+v, want unit %d/QUEUED", i, item, unitIDs[i])
		}
	}

	units, err := sessions.ListUnits(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("ListUnits: %v", err)
	}
	for _, u := range units {
		if u.PackID == nil || *u.PackID != packID {
			t.Errorf("unit %d PackID = %v, want %d", u.ID, u.PackID, packID)
		}
	}
}

func TestPacks_ResolveItemMovesQueuedToResolvedAndRecomputesCounts(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	packs := library.NewPacks(opened)
	sessionID, unitIDs := seedSessionWithUnits(t, sessions, 2)
	work := createWork(t, opened, "music", "Fake Album")

	items := []library.NewPackItem{
		{ImportUnitID: unitIDs[0], State: library.PackItemQueued},
		{ImportUnitID: unitIDs[1], State: library.PackItemQueued},
	}
	packID, err := packs.Create(t.Context(), library.PackDiscography, library.PackDetectedFromStructureAndTitle,
		0.85, "Artist", sessionID, 200, items)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := packs.ResolveItem(t.Context(), unitIDs[0], work.ID, nil); err != nil {
		t.Fatalf("ResolveItem: %v", err)
	}

	got, err := packs.Get(t.Context(), packID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ResolvedCount != 1 || got.MemberCount != 2 {
		t.Errorf("pack = %+v, want ResolvedCount=1 MemberCount=2", got)
	}
	packItems, err := packs.Items(t.Context(), packID)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if packItems[0].State != library.PackItemResolved || packItems[0].WorkID == nil || *packItems[0].WorkID != work.ID {
		t.Errorf("item[0] = %+v, want RESOLVED/work %d", packItems[0], work.ID)
	}
	if packItems[1].State != library.PackItemQueued {
		t.Errorf("item[1] = %+v, want still QUEUED", packItems[1])
	}
}

// ResolveItem is a safe no-op for a unit that was never part of any pack --
// its own doc comment's own contract.
func TestPacks_ResolveItemIsANoOpForAUnitWithNoPack(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	packs := library.NewPacks(opened)
	_, unitIDs := seedSessionWithUnits(t, sessions, 1)
	work := createWork(t, opened, "music", "Fake Album")

	if err := packs.ResolveItem(t.Context(), unitIDs[0], work.ID, nil); err != nil {
		t.Errorf("ResolveItem = %v, want nil (no-op)", err)
	}
}

func TestPacks_GetAnswersNotFoundForAnUnknownID(t *testing.T) {
	opened, _ := newLibrary(t)
	packs := library.NewPacks(opened)
	_, err := packs.Get(t.Context(), 999)
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestPacks_ListPagePaginates(t *testing.T) {
	opened, _ := newLibrary(t)
	sessions := library.NewImportSessions(opened)
	packs := library.NewPacks(opened)

	var created []int64
	for i := 0; i < 3; i++ {
		sessionID, unitIDs := seedSessionWithUnits(t, sessions, 1)
		items := []library.NewPackItem{{ImportUnitID: unitIDs[0], State: library.PackItemQueued}}
		id, err := packs.Create(t.Context(), library.PackDiscography, library.PackDetectedFromStructureAndTitle,
			0.85, "Artist", sessionID, 200, items)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created = append(created, id)
	}

	page, err := packs.ListPage(t.Context(), 0, 2)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(page) != 2 || page[0].ID != created[0] || page[1].ID != created[1] {
		t.Fatalf("page = %+v, want the first two", page)
	}

	rest, err := packs.ListPage(t.Context(), page[len(page)-1].ID, 2)
	if err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if len(rest) != 1 || rest[0].ID != created[2] {
		t.Fatalf("rest = %+v, want the third", rest)
	}
}
