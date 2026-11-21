package importjob

import (
	"context"
	"path"

	"github.com/makearr/Solarr/internal/db/library"
	importbatch "github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// releaseNotes reads what a grabbed release said about itself, so the import keeps it with the files it brought (QUAL-1).
// The zero value records nothing.
type releaseNotes struct {
	acquisitions importbatch.AcquisitionSource
	clock        clock.Clock
}

// originOf is what to keep for the files an importer placed from a download, or from a folder that is named like a
// release; nil when the media type cannot read release names or the name says nothing.
func (n releaseNotes) originOf(ctx context.Context, importer importbatch.Importer, acquisitionID *int64, sourcePath string) *library.ReleaseOrigin {
	reader, ok := importer.(importbatch.ReleaseReader)
	if !ok || n.clock == nil {
		return nil
	}
	origin := library.ReleaseOrigin{Title: path.Base(sourcePath), GrabbedAt: n.clock.Now().Unix()}
	if acquisitionID != nil {
		if n.acquisitions == nil {
			return nil
		}
		acquisition, err := n.acquisitions.Get(ctx, *acquisitionID)
		if err != nil || acquisition.CandidateTitle == "" {
			return nil
		}
		origin.Title, origin.IndexerID, origin.GrabbedAt = acquisition.CandidateTitle, acquisition.IndexerID, acquisition.CreatedAt
	}
	facts, ok := reader.ReleaseFactsOf(origin.Title)
	if !ok {
		return nil
	}
	origin.Source, origin.Group, origin.Revision = facts.Source, facts.Group, facts.Revision
	return &origin
}
