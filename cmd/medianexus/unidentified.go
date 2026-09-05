package main

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
	"github.com/makearr/MediaNexus/internal/paginate"
)

// unidentifiedAdapter satisfies apiserver.UnidentifiedReader over
// library.Unidentified. Neither package imports the other (worksAdapter's doc
// comment explains why), so something in between has to translate.
type unidentifiedAdapter struct{ unidentified library.Unidentified }

func (a unidentifiedAdapter) ListUnidentified(
	ctx context.Context, rootFolderIDs []int64, cursor paginate.Cursor, pageSize int,
) ([]apiserver.UnidentifiedRow, error) {
	rows, err := a.unidentified.List(ctx, rootFolderIDs, cursor, pageSize)
	if err != nil {
		return nil, err
	}

	views := make([]apiserver.UnidentifiedRow, len(rows))
	for index, row := range rows {
		views[index] = apiserver.UnidentifiedRow{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
			Candidates:  toCandidateRows(row.Candidates),
			FirstSeenAt: time.Unix(row.FirstSeenAt, 0), LastSeenAt: time.Unix(row.LastSeenAt, 0),
		}
	}
	return views, nil
}

// unidentifiedResolverAdapter satisfies apiserver.UnidentifiedResolver over
// music.ResolveUnidentified -- worksAdapter's own doc comment explains why
// something has to sit between the two packages.
type unidentifiedResolverAdapter struct {
	unidentified library.Unidentified
	identity     library.Identity
	provider     musicbrainz.Provider
	clock        clock.Clock
}

func (a unidentifiedResolverAdapter) GetUnidentified(
	ctx context.Context, mediaObjectID int64,
) (apiserver.UnidentifiedRow, error) {
	row, err := a.unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return apiserver.UnidentifiedRow{}, err
	}
	return apiserver.UnidentifiedRow{
		MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
		Candidates:  toCandidateRows(row.Candidates),
		FirstSeenAt: time.Unix(row.FirstSeenAt, 0), LastSeenAt: time.Unix(row.LastSeenAt, 0),
	}, nil
}

func (a unidentifiedResolverAdapter) ResolveUnidentified(
	ctx context.Context, mediaObjectID int64, provider, externalID string,
) (int64, error) {
	workID, err := music.ResolveUnidentified(ctx, a.clock, a.provider, a.identity, a.unidentified,
		mediaObjectID, provider, externalID)
	if err != nil {
		return 0, translateResolveError(err)
	}
	return workID, nil
}

func translateResolveError(err error) error {
	if errors.Is(err, music.ErrUnsupportedProvider) {
		return apiserver.ErrUnsupportedProvider
	}
	return err
}

func toCandidateRows(from []library.Candidate) []apiserver.UnidentifiedCandidate {
	views := make([]apiserver.UnidentifiedCandidate, len(from))
	for index, candidate := range from {
		views[index] = apiserver.UnidentifiedCandidate{
			Strategy: candidate.Strategy, Provider: candidate.Provider,
			ExternalID: candidate.ExternalID, Confidence: candidate.Confidence,
		}
	}
	return views
}
