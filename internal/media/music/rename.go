package music

import (
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/moving/naming"
)

// RenameTarget is one file's current and proposed location (section 11.8 preview as a diff, Invariant 73); WorkID and SizeBytes spare callers re-deriving them.
type RenameTarget struct {
	MediaObjectID   int64
	WorkID          int64
	SizeBytes       int64
	CurrentRelPath  string
	ProposedRelPath string
	// Warnings are naming's non-fatal notices; the rename preview is the one place they can reach the user before anything moves.
	Warnings []naming.Warning
}

// Changed reports whether the proposed path differs from the current one --
// a rename preview only lists targets where this is true.
func (t RenameTarget) Changed() bool { return t.ProposedRelPath != t.CurrentRelPath }

// ErrNoIdentity means the file has no work_part identity to name it by, or
// the identity it does have does not resolve against detail (a caller
// passed a mismatched WorkDetail).
var ErrNoIdentity = errors.New("music: file has no work_part identity")

// RenameTargetFor computes one file's proposed path under its library's naming. An absent
// identity.EditionID renders with a zero EditionDetail: the work's year, no medium format.
func RenameTargetFor(
	names Naming, detail library.WorkDetail, identity library.FileIdentity,
	mediaObjectID, sizeBytes int64, currentRelPath string,
) (RenameTarget, error) {
	var edition library.EditionDetail
	if identity.EditionID != nil {
		for _, candidate := range detail.Editions {
			if candidate.Edition.ID == *identity.EditionID {
				edition = candidate
				break
			}
		}
	}

	var part library.PartDetail
	found := false
	for _, candidate := range detail.Parts {
		if candidate.Part.MediaRef == identity.MediaRef {
			part = candidate
			found = true
			break
		}
	}
	if !found {
		return RenameTarget{}, fmt.Errorf("%w: work %d has no part for media_ref %q",
			ErrNoIdentity, detail.Work.ID, identity.MediaRef)
	}

	facts, err := NamingContext(detail, edition, part, currentRelPath)
	if err != nil {
		return RenameTarget{}, fmt.Errorf("music: naming context for media_object %d: %w", mediaObjectID, err)
	}

	proposed, warnings, err := names.RenderTrack(facts, DiscCount(detail.Parts), currentRelPath)
	if err != nil {
		return RenameTarget{}, fmt.Errorf("music: render path for media_object %d: %w", mediaObjectID, err)
	}

	return RenameTarget{
		MediaObjectID: mediaObjectID, WorkID: identity.WorkID, SizeBytes: sizeBytes,
		CurrentRelPath: currentRelPath, ProposedRelPath: proposed, Warnings: warnings,
	}, nil
}
