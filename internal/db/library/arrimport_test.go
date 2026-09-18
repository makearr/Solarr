package library_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestArrImports_StartAddItemFinishRoundTrips(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)
	imports := library.NewArrImports(opened)

	importID, err := imports.Start(t.Context(), 7, "lidarr", 1000)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	run, err := imports.Get(t.Context(), importID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.State != library.ArrImportRunning || run.InstanceID != 7 || run.App != "lidarr" {
		t.Fatalf("run = %+v, want RUNNING for instance 7/lidarr", run)
	}

	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")
	itemID, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: "lidarr-artist-1", Title: "Vutu Doci",
		PersonID: &personID, PersonCreated: true,
	}, 1001)
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	items, err := imports.ListItems(t.Context(), importID)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != itemID || items[0].PersonID == nil || *items[0].PersonID != personID {
		t.Fatalf("items = %+v, want one artist item naming person %d", items, personID)
	}
	if !items[0].PersonCreated {
		t.Error("PersonCreated = false, want true")
	}

	if err := imports.Finish(t.Context(), importID, library.ArrImportDone, 1002, "",
		library.ArrImportCounts{ArtistsCreated: 1}); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	finished, err := imports.Get(t.Context(), importID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if finished.State != library.ArrImportDone || finished.ArtistsCreated != 1 {
		t.Errorf("finished run = %+v, want DONE with ArtistsCreated=1", finished)
	}
	if finished.FinishedAt == nil || *finished.FinishedAt != 1002 {
		t.Errorf("FinishedAt = %v, want 1002", finished.FinishedAt)
	}

	runs, err := imports.ListForInstance(t.Context(), 7)
	if err != nil {
		t.Fatalf("ListForInstance: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != importID {
		t.Fatalf("ListForInstance = %+v, want the one run", runs)
	}
}

// A run that created a person, its subscription and a work must have all
// three gone after Revert, and the run itself marked REVERTED -- the same
// "only what this run created, never what it only matched" contract
// library.ImportRevert already holds for a normal import, one level up.
func TestArrImportRevert_DeletesWhatTheRunCreated(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	subscriptions := library.NewSubscriptions(opened)
	imports := library.NewArrImports(opened)
	identity := library.NewIdentity(opened)

	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")
	subscriptionID, err := people.Subscribe(t.Context(), firstSeen, personID, 0,
		library.SubscriptionRequest{MediaTypes: []string{"music"}, Roles: []string{"artist"}})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	work := createWork(t, opened, "music", "The Sezo Siluko")

	importID, err := imports.Start(t.Context(), 1, "lidarr", firstSeen.Unix())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: "lidarr-artist-1", Title: "Vutu Doci",
		PersonID: &personID, PersonCreated: true, SubscriptionID: &subscriptionID,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (artist): %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemAlbum, SourceID: "lidarr-album-1", Title: "The Sezo Siluko",
		WorkID: &work.ID, WorkCreated: true,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (album): %v", err)
	}
	if err := imports.Finish(t.Context(), importID, library.ArrImportDone, firstSeen.Unix(), "",
		library.ArrImportCounts{ArtistsCreated: 1, AlbumsCreated: 1}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	revert := library.NewArrImportRevert(imports, identity, people, subscriptions)
	result, err := revert.Revert(t.Context(), importID)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Revert errors = %v, want none", result.Errors)
	}
	if result.WorksDeleted != 1 || result.SubscriptionsDeleted != 1 || result.PeopleDeleted != 1 {
		t.Errorf("result = %+v, want one of each deleted", result)
	}

	if _, err := queries.GetWork(t.Context(), work.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetWork after revert = %v, want sql.ErrNoRows", err)
	}
	if _, err := queries.GetPerson(t.Context(), personID); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetPerson after revert = %v, want sql.ErrNoRows", err)
	}
	remaining, err := queries.ListEnabledSubscriptions(t.Context())
	if err != nil {
		t.Fatalf("ListEnabledSubscriptions: %v", err)
	}
	for _, row := range remaining {
		if row.ID == subscriptionID {
			t.Error("subscription still enabled after revert, want deleted")
		}
	}

	run, err := imports.Get(t.Context(), importID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if run.State != library.ArrImportReverted {
		t.Errorf("run.State = %q, want REVERTED", run.State)
	}
}

// A revert of a work or person the run only matched (WorkCreated/
// PersonCreated false) must leave both untouched -- ArrImportRevert must
// never delete something it did not create, mirroring library.ImportRevert's
// own two-pass reasoning for a work two units both resolve to.
func TestArrImportRevert_NeverDeletesWhatTheRunOnlyMatched(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	subscriptions := library.NewSubscriptions(opened)
	imports := library.NewArrImports(opened)
	identity := library.NewIdentity(opened)

	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")
	work := createWork(t, opened, "music", "The Sezo Siluko")

	importID, err := imports.Start(t.Context(), 1, "lidarr", firstSeen.Unix())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: "lidarr-artist-1", Title: "Vutu Doci",
		PersonID: &personID, PersonCreated: false,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (artist): %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemAlbum, SourceID: "lidarr-album-1", Title: "The Sezo Siluko",
		WorkID: &work.ID, WorkCreated: false,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (album): %v", err)
	}
	if err := imports.Finish(t.Context(), importID, library.ArrImportDone, firstSeen.Unix(), "",
		library.ArrImportCounts{ArtistsMatched: 1, AlbumsMatched: 1}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	revert := library.NewArrImportRevert(imports, identity, people, subscriptions)
	result, err := revert.Revert(t.Context(), importID)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if result.WorksDeleted != 0 || result.PeopleDeleted != 0 {
		t.Errorf("result = %+v, want nothing deleted (matched, not created)", result)
	}

	if _, err := queries.GetWork(t.Context(), work.ID); err != nil {
		t.Errorf("GetWork after revert = %v, want the matched work still present", err)
	}
	if _, err := queries.GetPerson(t.Context(), personID); err != nil {
		t.Errorf("GetPerson after revert = %v, want the matched person still present", err)
	}
}

// A person this run created and set as a work it also created's primary
// artist (AddCredit's own role="artist" ord=0 case, work.primary_artist_id)
// must still delete cleanly: work.primary_artist_id carries no ON DELETE
// clause of its own, so deleting the person while that reference still
// stands would foreign-key-violate. Revert must delete the work -- clearing
// the reference -- before the person, not the other way round.
func TestArrImportRevert_DeletesTheWorkBeforeItsOwnPrimaryArtist(t *testing.T) {
	opened, queries := newLibrary(t)
	people := library.NewPeople(opened)
	subscriptions := library.NewSubscriptions(opened)
	imports := library.NewArrImports(opened)
	identity := library.NewIdentity(opened)

	personID := resolvePersonNamed(t, people, "artist-1", "Vutu Doci", "Bush, Vutu")
	work := createWork(t, opened, "music", "The Sezo Siluko")
	if err := people.AddCredit(t.Context(), work.ID, personID, "artist", 0); err != nil {
		t.Fatalf("AddCredit: %v", err)
	}
	if row, err := queries.GetWork(t.Context(), work.ID); err != nil || !row.PrimaryArtistID.Valid {
		t.Fatalf("work.primary_artist_id after AddCredit = %+v, err=%v, want it set (test setup)", row, err)
	}

	importID, err := imports.Start(t.Context(), 1, "lidarr", firstSeen.Unix())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: "lidarr-artist-1", Title: "Vutu Doci",
		PersonID: &personID, PersonCreated: true,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (artist): %v", err)
	}
	if _, err := imports.AddItem(t.Context(), importID, library.NewArrImportItem{
		Kind: library.ArrImportItemAlbum, SourceID: "lidarr-album-1", Title: "The Sezo Siluko",
		WorkID: &work.ID, WorkCreated: true,
	}, firstSeen.Unix()); err != nil {
		t.Fatalf("AddItem (album): %v", err)
	}
	if err := imports.Finish(t.Context(), importID, library.ArrImportDone, firstSeen.Unix(), "",
		library.ArrImportCounts{ArtistsCreated: 1, AlbumsCreated: 1}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	revert := library.NewArrImportRevert(imports, identity, people, subscriptions)
	result, err := revert.Revert(t.Context(), importID)
	if err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("Revert errors = %v, want none (the work must delete before its own primary artist)", result.Errors)
	}
	if result.WorksDeleted != 1 || result.PeopleDeleted != 1 {
		t.Errorf("result = %+v, want one work and one person deleted", result)
	}
}
