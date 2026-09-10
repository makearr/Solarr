package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/paginate"
)

// Unidentified satisfies apiserver.UnidentifiedReader over library.Unidentified.
type Unidentified struct{ Unidentified library.Unidentified }

func (a Unidentified) ListUnidentified(
	ctx context.Context, rootFolderIDs []int64, cursor paginate.Cursor, pageSize int,
) ([]apiserver.UnidentifiedRow, error) {
	rows, err := a.Unidentified.List(ctx, rootFolderIDs, cursor, pageSize)
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

// UnidentifiedResolver satisfies apiserver.UnidentifiedResolver over music.ResolveUnidentified.
type UnidentifiedResolver struct {
	Unidentified library.Unidentified
	Identity     library.Identity
	Provider     musicbrainz.Provider
	Clock        clock.Clock
}

func (a UnidentifiedResolver) GetUnidentified(
	ctx context.Context, mediaObjectID int64,
) (apiserver.UnidentifiedRow, error) {
	row, err := a.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return apiserver.UnidentifiedRow{}, err
	}
	return apiserver.UnidentifiedRow{
		MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
		Candidates:  toCandidateRows(row.Candidates),
		FirstSeenAt: time.Unix(row.FirstSeenAt, 0), LastSeenAt: time.Unix(row.LastSeenAt, 0),
	}, nil
}

func (a UnidentifiedResolver) ResolveUnidentified(
	ctx context.Context, mediaObjectID int64, provider, externalID string,
) (int64, error) {
	workID, err := music.ResolveUnidentified(ctx, a.Clock, a.Provider, a.Identity, a.Unidentified,
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
