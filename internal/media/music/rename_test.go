package music_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
	"github.com/makearr/MediaNexus/internal/media/music"
)

func workDetailFixture() library.WorkDetail {
	return library.WorkDetail{
		Work: librarydb.Work{
			ID: 1, CanonicalTitle: "Tender Prey", Year: sql.NullInt64{Int64: 1988, Valid: true},
		},
		Editions: []library.EditionDetail{
			{Edition: librarydb.Edition{
				ID: 10, WorkID: 1, Year: sql.NullInt64{Int64: 2010, Valid: true},
				Format: sql.NullString{String: "FLAC", Valid: true},
			}},
		},
		Parts: []library.PartDetail{
			{Part: librarydb.WorkPart{
				ID: 100, WorkID: 1, MediaRef: "disc1:track3",
				Title: sql.NullString{String: "The Furab Keke", Valid: true},
			}},
		},
		Credits: []library.Credit{{PersonName: "Nick Cave and the Bad Seeds", Role: music.CreditRoleArtist}},
	}
}

func TestRenameTargetFor_RendersUnderTheDefaultTemplate(t *testing.T) {
	detail := workDetailFixture()
	identity := library.FileIdentity{WorkID: 1, MediaRef: "disc1:track3"}

	target, err := music.RenameTargetFor(detail, identity, 42, 4096, "Old Path/track.flac")
	if err != nil {
		t.Fatalf("RenameTargetFor: %v", err)
	}
	want := "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 03 - The Furab Keke.flac"
	if target.ProposedRelPath != want {
		t.Errorf("ProposedRelPath = %q, want %q", target.ProposedRelPath, want)
	}
	if target.MediaObjectID != 42 || target.CurrentRelPath != "Old Path/track.flac" {
		t.Errorf("target = %+v, unexpected MediaObjectID/CurrentRelPath", target)
	}
	if !target.Changed() {
		t.Error("Changed() = false, want true: the current path does not match the proposed one")
	}
}

// A track with no MediaRef in identity.EditionID (an "any pressing" SET)
// still renders, falling back to the work's own year since no specific
// edition is pinned to supply one.
func TestRenameTargetFor_FallsBackToWorkYearWithNoEdition(t *testing.T) {
	detail := workDetailFixture()
	identity := library.FileIdentity{WorkID: 1, MediaRef: "disc1:track3"}

	target, err := music.RenameTargetFor(detail, identity, 42, 4096, "track.flac")
	if err != nil {
		t.Fatalf("RenameTargetFor: %v", err)
	}
	if target.ProposedRelPath != "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 03 - The Furab Keke.flac" {
		t.Errorf("ProposedRelPath = %q, want the work's own 1988 year, not the edition's 2010", target.ProposedRelPath)
	}
}

// A pinned edition's own year is preferred over the work's -- a reissue
// folder conventionally names the pressing's own year, not the original.
func TestRenameTargetFor_PrefersThePinnedEditionYear(t *testing.T) {
	detail := workDetailFixture()
	editionID := int64(10)
	identity := library.FileIdentity{WorkID: 1, EditionID: &editionID, MediaRef: "disc1:track3"}

	target, err := music.RenameTargetFor(detail, identity, 42, 4096, "track.flac")
	if err != nil {
		t.Fatalf("RenameTargetFor: %v", err)
	}
	if target.ProposedRelPath != "Nick Cave and the Bad Seeds/Tender Prey (2010)/Nick Cave and the Bad Seeds - 03 - The Furab Keke.flac" {
		t.Errorf("ProposedRelPath = %q, want the pinned edition's own 2010 year", target.ProposedRelPath)
	}
}

// A path already at its proposed location reports Changed() == false, so a
// preview does not list it as a rename.
func TestRenameTargetFor_ReportsUnchangedWhenAlreadyCorrect(t *testing.T) {
	detail := workDetailFixture()
	identity := library.FileIdentity{WorkID: 1, MediaRef: "disc1:track3"}
	current := "Nick Cave and the Bad Seeds/Tender Prey (1988)/Nick Cave and the Bad Seeds - 03 - The Furab Keke.flac"

	target, err := music.RenameTargetFor(detail, identity, 42, 4096, current)
	if err != nil {
		t.Fatalf("RenameTargetFor: %v", err)
	}
	if target.Changed() {
		t.Error("Changed() = true, want false: the file is already at its proposed path")
	}
}

// A media_ref naming no real part in this work's own WorkDetail is a caller
// error (a mismatched identity/detail pair), not a silently empty result.
func TestRenameTargetFor_ErrorsWhenNoPartMatchesTheMediaRef(t *testing.T) {
	detail := workDetailFixture()
	identity := library.FileIdentity{WorkID: 1, MediaRef: "disc9:track99"}

	_, err := music.RenameTargetFor(detail, identity, 42, 4096, "track.flac")
	if !errors.Is(err, music.ErrNoIdentity) {
		t.Errorf("RenameTargetFor = %v, want ErrNoIdentity", err)
	}
}
