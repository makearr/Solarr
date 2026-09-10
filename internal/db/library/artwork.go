package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// ArtworkCandidate is a work with a release MBID a cover art provider can be
// queried with, but no recorded cover yet (section 21.6).
type ArtworkCandidate struct {
	WorkID      int64
	ReleaseMBID string
}

// Artworks is section 21.6's persistence half: which works still need cover art,
// and recording what was fetched. Fetching and encoding are internal/platform/artwork's
// and a provider client's job, not this package's (ADR-23: core carries no
// media-type or provider logic).
type Artworks struct{ store *store.Store }

// NewArtworks builds the wrapper.
func NewArtworks(opened *store.Store) Artworks { return Artworks{store: opened} }

// artworkKindCover is the only kind this pass populates; poster/backdrop are
// video-shaped and have no source yet.
const artworkKindCover = "cover"

// NeedingArtwork returns up to pageSize works with a usable release MBID and no
// recorded cover, lowest work id first. The result set shrinks as Record
// succeeds, the same no-cursor-needed shape library.PlaceholderEditions uses.
func (a Artworks) NeedingArtwork(ctx context.Context, pageSize int64) ([]ArtworkCandidate, error) {
	rows, err := librarydb.New(a.store.Read()).ListWorksNeedingArtwork(ctx, pageSize)
	if err != nil {
		return nil, fmt.Errorf("library: works needing artwork: %w", err)
	}
	candidates := make([]ArtworkCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = ArtworkCandidate{WorkID: row.WorkID, ReleaseMBID: row.ReleaseMbid}
	}
	return candidates, nil
}

// WorkArtwork is what a caller needs to serve a work's cover art file itself:
// the cache key ArtworkJob wrote it under, and the root folders it must check
// the caller can see (the same ADR-27 visibility GetWorkDetail already applies,
// reused here rather than requiring a full work-detail fetch just to serve an
// image).
type WorkArtwork struct {
	CacheKey      string
	Found         bool
	RootFolderIDs []int64
}

// GetForWork answers a work's recorded cover art, if any. Found is false (not
// an error) when the work has no cover recorded yet -- the common case for a
// work still waiting on ArtworkJob, not a failure.
func (a Artworks) GetForWork(ctx context.Context, workID int64) (WorkArtwork, error) {
	queries := librarydb.New(a.store.Read())
	rootFolderIDs, err := queries.ListRootFoldersForWork(ctx, workID)
	if err != nil {
		return WorkArtwork{}, fmt.Errorf("library: root folders for work %d: %w", workID, err)
	}

	cacheKey, err := queries.GetArtwork(ctx, librarydb.GetArtworkParams{
		SubjectType: "work", SubjectID: workID, Kind: artworkKindCover,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return WorkArtwork{RootFolderIDs: rootFolderIDs}, nil
	}
	if err != nil {
		return WorkArtwork{}, fmt.Errorf("library: artwork for work %d: %w", workID, err)
	}
	return WorkArtwork{CacheKey: cacheKey, Found: true, RootFolderIDs: rootFolderIDs}, nil
}

// Record upserts the cover art row for a work (ADR-2: the row is persistent, the
// cache/images/ bytes behind cacheKey are disposable and rebuild from sourceURL).
func (a Artworks) Record(
	ctx context.Context, workID int64, sourceURL, cacheKey string, width, height int, fetchedAt int64,
) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(a.store.Read()).WithTx(tx).UpsertArtwork(ctx, librarydb.UpsertArtworkParams{
			SubjectType: "work",
			SubjectID:   workID,
			Kind:        artworkKindCover,
			SourceUrl:   sql.NullString{String: sourceURL, Valid: sourceURL != ""},
			CacheKey:    cacheKey,
			Width:       sql.NullInt64{Int64: int64(width), Valid: width > 0},
			Height:      sql.NullInt64{Int64: int64(height), Valid: height > 0},
			FetchedAt:   sql.NullInt64{Int64: fetchedAt, Valid: true},
		})
	})
}
