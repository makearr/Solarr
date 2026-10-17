package adapters

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
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

// UnidentifiedResolver satisfies apiserver.UnidentifiedResolver: a file is placed through its library's media type.
type UnidentifiedResolver struct {
	Unidentified library.Unidentified
	Attempts     library.Attempts
	Cache        *settings.Cache
	PlacingFiles func(mediaType string) (media.PlacingFiles, bool)
	Clock        clock.Clock
}

// MusicFiles is music's media.PlacingFiles over music.ResolveUnidentified: a file becomes part of the album named.
type MusicFiles struct {
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

// ResolveUnidentified places the file in the library named, or the first of its root folder's libraries.
func (a UnidentifiedResolver) ResolveUnidentified(
	ctx context.Context, mediaObjectID int64, categoryID *int64, placement apiserver.FilePlacement, wholeFolder bool,
) (workID, resolved int64, err error) {
	entry, err := a.Unidentified.Get(ctx, mediaObjectID)
	if err != nil {
		return 0, 0, err
	}
	snapshot := a.Cache.Snapshot()
	libraries := snapshot.CategoriesForRootFolder(entry.RootFolderID)
	if categoryID == nil && len(libraries) > 0 {
		categoryID = &libraries[0]
	}
	if categoryID == nil || !slices.Contains(libraries, *categoryID) {
		return 0, 0, fmt.Errorf("%w: the file is in no such library", apiserver.ErrInvalidChoice)
	}
	category, _ := snapshot.Category(*categoryID)
	placing, ok := a.PlacingFiles(category.MediaType)
	if !ok {
		return 0, 0, apiserver.ErrNotBuiltForMediaType
	}
	workID, resolved, err = placing.PlaceFile(ctx, *categoryID, mediaObjectID, media.Placement{
		Provider: placement.Provider, ExternalID: placement.ExternalID, Group: placement.Group, Parts: placement.Parts,
	}, wholeFolder)
	if errors.Is(err, media.ErrNotWired) {
		return workID, resolved, apiserver.ErrNotBuiltForMediaType
	}
	if errors.Is(err, media.ErrInvalidAdd) {
		return workID, resolved, fmt.Errorf("%w: %w", apiserver.ErrInvalidChoice, err)
	}
	return workID, resolved, err
}

// PlaceFile implements media.PlacingFiles for music; an album has no groups, so the placement's are not read.
func (a MusicFiles) PlaceFile(
	ctx context.Context, _, mediaObjectID int64, placement media.Placement, wholeFolder bool,
) (workID, resolved int64, err error) {
	provider, externalID := placement.Provider, placement.ExternalID
	ids := []int64{mediaObjectID}
	if wholeFolder {
		entry, err := a.Unidentified.Get(ctx, mediaObjectID)
		if err != nil {
			return 0, 0, err
		}
		if ids, err = a.Unidentified.InFolder(ctx, entry.RootFolderID, entry.RelPath); err != nil {
			return 0, 0, err
		}
	}
	for _, id := range ids {
		resolvedWorkID, err := music.ResolveUnidentified(ctx, a.Clock, a.Provider, a.Identity, a.Unidentified,
			id, provider, externalID)
		if err != nil {
			return workID, resolved, translateResolveError(err)
		}
		workID = resolvedWorkID
		resolved++
	}
	return workID, resolved, nil
}

func (a UnidentifiedResolver) MarkNotMedia(ctx context.Context, mediaObjectID int64) error {
	return a.Unidentified.MarkNotMedia(ctx, mediaObjectID, a.Clock.Now().Unix())
}

func (a UnidentifiedResolver) IdentificationAttempts(
	ctx context.Context, mediaObjectID int64,
) ([]apiserver.IdentificationAttempt, error) {
	attempts, err := a.Attempts.List(ctx, mediaObjectID)
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.IdentificationAttempt, len(attempts))
	for i, attempt := range attempts {
		passes := make([]apiserver.IdentificationPass, len(attempt.Passes))
		for j, pass := range attempt.Passes {
			candidates := make([]apiserver.IdentificationCandidate, len(pass.Candidates))
			for k, candidate := range pass.Candidates {
				evidence := make([]apiserver.IdentificationEvidence, len(candidate.Evidence))
				for n, fact := range candidate.Evidence {
					evidence[n] = apiserver.IdentificationEvidence{Kind: fact.Kind, Detail: fact.Detail}
				}
				candidates[k] = apiserver.IdentificationCandidate{
					Strategy: candidate.Strategy, Provider: candidate.Provider, ExternalID: candidate.ExternalID,
					Confidence: candidate.Confidence, Evidence: evidence,
				}
			}
			passes[j] = apiserver.IdentificationPass{Pass: pass.Pass, Outcome: pass.Outcome, Candidates: candidates}
		}
		views[i] = apiserver.IdentificationAttempt{At: time.Unix(attempt.At, 0), Outcome: attempt.Outcome, Passes: passes}
	}
	return views, nil
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
			ExternalID: candidate.ExternalID, Confidence: candidate.Confidence, Title: candidate.Title,
		}
	}
	return views
}
