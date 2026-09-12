package music

import (
	"errors"
	"fmt"
	"path"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/moving/naming"
)

// compiledDefaultFolder and compiledDefaultFile are the shipped default
// template (DefaultFolderTemplate/DefaultFileTemplate), compiled once --
// section 11.8's own "compilation is validation": a template that fails to
// compile is a build-time bug, not a runtime surprise, so a compile error
// here panics at package init rather than being silently swallowed per
// call. A naming_profile system (todo 4.5's own later follow-up) would
// compile per-category templates the same way, cached, instead of this one
// fixed pair.
var compiledDefaultFolder, compiledDefaultFile = mustCompileDefaults()

func mustCompileDefaults() ([]naming.Segment, []naming.Segment) {
	folder, err := naming.Compile(DefaultFolderTemplate)
	if err != nil {
		panic(fmt.Sprintf("music: default folder template does not compile: %v", err))
	}
	file, err := naming.Compile(DefaultFileTemplate)
	if err != nil {
		panic(fmt.Sprintf("music: default file template does not compile: %v", err))
	}
	return folder, file
}

// RenameTarget is one file's current and proposed location under the
// shipped default naming template -- section 11.8's own "preview as a
// diff" (Invariant 73), scoped to the one template that exists today.
// WorkID and SizeBytes carry through what RenamePreview already resolved
// per file, so a caller building a mover.Request from a []RenameTarget
// never needs to re-derive either.
type RenameTarget struct {
	MediaObjectID   int64
	WorkID          int64
	SizeBytes       int64
	CurrentRelPath  string
	ProposedRelPath string
	// Warnings are naming.RenderPath's own non-fatal notices about the
	// proposed path (today: only the total-length recommendation) -- the
	// rename preview is a genuine diff-before-apply step (Invariant 73), so
	// this is the one caller where a warning can actually reach the user
	// before anything moves.
	Warnings []naming.Warning
}

// Changed reports whether the proposed path differs from the current one --
// a rename preview only lists targets where this is true.
func (t RenameTarget) Changed() bool { return t.ProposedRelPath != t.CurrentRelPath }

// ErrNoIdentity means the file has no work_part identity to name it by, or
// the identity it does have does not resolve against detail (a caller
// passed a mismatched WorkDetail).
var ErrNoIdentity = errors.New("music: file has no work_part identity")

// RenameTargetFor computes one file's proposed path under the shipped
// default template, from the identity library.Desired.IdentityForFile
// already resolved and the WorkDetail it names. identity.EditionID absent
// (an "any pressing" SET) renders with a zero-value EditionDetail --
// NamingContext's own releaseYear falls back to the work's year, and
// Format is simply absent from the rendered path, the same way it already
// is for any file where the edition does not state one.
func RenameTargetFor(
	detail library.WorkDetail, identity library.FileIdentity, mediaObjectID, sizeBytes int64, currentRelPath string,
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

	ctx, err := NamingContext(detail, edition, part)
	if err != nil {
		return RenameTarget{}, fmt.Errorf("music: naming context for media_object %d: %w", mediaObjectID, err)
	}

	extension := path.Ext(currentRelPath)
	proposed, warnings, err := naming.RenderPath(compiledDefaultFolder, compiledDefaultFile, ctx, naming.DefaultSeparator, extension)
	if err != nil {
		return RenameTarget{}, fmt.Errorf("music: render path for media_object %d: %w", mediaObjectID, err)
	}

	return RenameTarget{
		MediaObjectID: mediaObjectID, WorkID: identity.WorkID, SizeBytes: sizeBytes,
		CurrentRelPath: currentRelPath, ProposedRelPath: proposed, Warnings: warnings,
	}, nil
}
