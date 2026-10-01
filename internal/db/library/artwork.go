package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ArtworkCandidate is a work with no recorded cover yet and an id a cover art
// provider can be queried with (section 21.6): a release MBID from
// NeedingArtwork, a release-group MBID from NeedingReleaseGroupArtwork.
type ArtworkCandidate struct {
	WorkID int64
	MBID   string
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
func (a Artworks) NeedingArtwork(ctx context.Context, afterWorkID, pageSize int64) ([]ArtworkCandidate, error) {
	rows, err := librarydb.New(a.store.Read()).ListWorksNeedingArtwork(ctx, librarydb.ListWorksNeedingArtworkParams{
		AfterID: afterWorkID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: works needing artwork: %w", err)
	}
	candidates := make([]ArtworkCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = ArtworkCandidate{WorkID: row.WorkID, MBID: row.ReleaseMbid}
	}
	return candidates, nil
}

// NeedingReleaseGroupArtwork returns up to pageSize works after afterWorkID
// still without a cover that carry a release-group id.
func (a Artworks) NeedingReleaseGroupArtwork(ctx context.Context, afterWorkID, pageSize int64) ([]ArtworkCandidate, error) {
	rows, err := librarydb.New(a.store.Read()).ListWorksNeedingReleaseGroupArtwork(ctx,
		librarydb.ListWorksNeedingReleaseGroupArtworkParams{AfterID: afterWorkID, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("library: works needing release group artwork: %w", err)
	}
	candidates := make([]ArtworkCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = ArtworkCandidate{WorkID: row.WorkID, MBID: row.ReleaseGroupMbid}
	}
	return candidates, nil
}

// EmbeddedArtworkCandidate is a work still without a cover and one of its
// files, by root folder and path, to read embedded art from.
type EmbeddedArtworkCandidate struct {
	WorkID       int64
	RootFolderID int64
	RelPath      string
}

// NeedingEmbeddedArtwork returns up to pageSize works after afterWorkID still
// without a cover that have a file recorded by work_file.
func (a Artworks) NeedingEmbeddedArtwork(ctx context.Context, afterWorkID, pageSize int64) ([]EmbeddedArtworkCandidate, error) {
	rows, err := librarydb.New(a.store.Read()).ListWorksNeedingEmbeddedArtwork(ctx,
		librarydb.ListWorksNeedingEmbeddedArtworkParams{AfterID: afterWorkID, PageSize: pageSize})
	if err != nil {
		return nil, fmt.Errorf("library: works needing embedded artwork: %w", err)
	}
	candidates := make([]EmbeddedArtworkCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = EmbeddedArtworkCandidate{WorkID: row.WorkID, RootFolderID: row.RootFolderID, RelPath: row.RelPath}
	}
	return candidates, nil
}

// PersonArtworkCandidate is a person with a MusicBrainz artist id to look an
// image up by, and no recorded image yet.
type PersonArtworkCandidate struct {
	PersonID   int64
	ArtistMBID string
}

// PeopleNeedingArtwork returns up to pageSize credited people carrying a
// MusicBrainz artist id and no recorded image, lowest person id first --
// NeedingArtwork's own shape one subject over. Works have had that sweep since
// section 21.6; people never did, so an artist first resolved before the image
// code existed never got one (found 2026-09-13).
func (a Artworks) PeopleNeedingArtwork(ctx context.Context, afterPersonID, pageSize int64) ([]PersonArtworkCandidate, error) {
	rows, err := librarydb.New(a.store.Read()).ListPeopleNeedingArtwork(ctx, librarydb.ListPeopleNeedingArtworkParams{
		AfterID: afterPersonID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: people needing artwork: %w", err)
	}
	candidates := make([]PersonArtworkCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = PersonArtworkCandidate{PersonID: row.PersonID, ArtistMBID: row.ArtistMbid}
	}
	return candidates, nil
}

// WorkArtwork is what a caller needs to serve a work's cover art file itself:
// the cache key ArtworkJob wrote it under, the root folders it must check
// the caller can see (the same ADR-27 visibility GetWorkDetail already applies,
// reused here rather than requiring a full work-detail fetch just to serve an
// image), and its primary artist -- the album-falls-back-to-artist-image
// chain asked for 2026-09-11, since a work with no cover of its own still
// answers with its own artist's when that one exists.
type WorkArtwork struct {
	CacheKey      string
	Found         bool
	RootFolderIDs []int64
	// CategoryIDs are the categories wanting this work, the other half of
	// ADR-27 visibility: a wanted album with no files has no root folder.
	CategoryIDs     []int64
	PrimaryArtistID *int64
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

	desired, err := queries.ListDesiredItemsForWork(ctx, workID)
	if err != nil {
		return WorkArtwork{}, fmt.Errorf("library: desired items for work %d: %w", workID, err)
	}
	categoryIDs := make([]int64, 0, len(desired))
	for _, item := range desired {
		if !slices.Contains(categoryIDs, item.CategoryID) {
			categoryIDs = append(categoryIDs, item.CategoryID)
		}
	}

	var primaryArtistID *int64
	if work, err := queries.GetWork(ctx, workID); err == nil && work.PrimaryArtistID.Valid {
		primaryArtistID = &work.PrimaryArtistID.Int64
	}

	found := WorkArtwork{RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, PrimaryArtistID: primaryArtistID}
	cacheKey, err := queries.GetArtwork(ctx, librarydb.GetArtworkParams{
		SubjectType: "work", SubjectID: workID, Kind: artworkKindCover,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return found, nil
	}
	if err != nil {
		return WorkArtwork{}, fmt.Errorf("library: artwork for work %d: %w", workID, err)
	}
	found.CacheKey, found.Found = cacheKey, true
	return found, nil
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

// PersonArtwork is what a caller needs to serve a person's own image: the
// cache key it was written under. Unlike WorkArtwork there is no
// RootFolderIDs to visibility-check against -- a person is a shared entity
// with no category-scoped access of its own (ADR-54's "the entity stays
// shared"; GetPerson itself has no such check either).
type PersonArtwork struct {
	CacheKey string
	Found    bool
}

// GetForPerson answers a person's recorded image, if any. Found is false
// (not an error) both when nobody has looked yet and when a look found
// nothing -- an empty cache_key is RecordPersonArtworkAbsent's own marker,
// and either way there is no file to serve.
func (a Artworks) GetForPerson(ctx context.Context, personID int64) (PersonArtwork, error) {
	cacheKey, err := librarydb.New(a.store.Read()).GetArtwork(ctx, librarydb.GetArtworkParams{
		SubjectType: "person", SubjectID: personID, Kind: artworkKindCover,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return PersonArtwork{}, nil
	}
	if err != nil {
		return PersonArtwork{}, fmt.Errorf("library: artwork for person %d: %w", personID, err)
	}
	if cacheKey == "" {
		return PersonArtwork{}, nil
	}
	return PersonArtwork{CacheKey: cacheKey, Found: true}, nil
}

// RecordPersonArtworkAbsent remembers that this person was looked up and has
// no image to fetch, as a row with an empty cache_key. Without it the
// backfill sweep re-asks about the same lowest-id people every run and never
// reaches the rest, since PeopleNeedingArtwork excludes only people who
// already have a row -- and unlike a cover art miss, each retry costs a
// MusicBrainz lookup against a 1 req/s budget shared with everything else.
func (a Artworks) RecordPersonArtworkAbsent(ctx context.Context, personID, checkedAt int64) error {
	return a.RecordForPerson(ctx, personID, "", "", 0, 0, checkedAt)
}

// RecordForPerson upserts the cover art row for a person -- Record's own
// twin, subject_type="person" instead of "work".
func (a Artworks) RecordForPerson(
	ctx context.Context, personID int64, sourceURL, cacheKey string, width, height int, fetchedAt int64,
) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(a.store.Read()).WithTx(tx).UpsertArtwork(ctx, librarydb.UpsertArtworkParams{
			SubjectType: "person",
			SubjectID:   personID,
			Kind:        artworkKindCover,
			SourceUrl:   sql.NullString{String: sourceURL, Valid: sourceURL != ""},
			CacheKey:    cacheKey,
			Width:       sql.NullInt64{Int64: int64(width), Valid: width > 0},
			Height:      sql.NullInt64{Int64: int64(height), Valid: height > 0},
			FetchedAt:   sql.NullInt64{Int64: fetchedAt, Valid: true},
		})
	})
}

// WorkMBIDs are the MusicBrainz ids a work's cover is looked up by.
type WorkMBIDs struct {
	ReleaseGroup string
	Releases     []string
}

// MBIDsForWork answers the release group and up to six releases of a work, either may be absent.
func (a Artworks) MBIDsForWork(ctx context.Context, workID int64) (WorkMBIDs, error) {
	queries := librarydb.New(a.store.Read())
	releaseGroup, err := queries.GetWorkReleaseGroupMBID(ctx, workID)
	if err != nil {
		return WorkMBIDs{}, fmt.Errorf("library: release group of work %d: %w", workID, err)
	}
	releases, err := queries.ListWorkReleaseMBIDs(ctx, workID)
	if err != nil {
		return WorkMBIDs{}, fmt.Errorf("library: releases of work %d: %w", workID, err)
	}
	return WorkMBIDs{ReleaseGroup: releaseGroup, Releases: releases}, nil
}

// ArtistMBID answers a person's MusicBrainz artist id, empty when it has none.
func (a Artworks) ArtistMBID(ctx context.Context, personID int64) (string, error) {
	mbid, err := librarydb.New(a.store.Read()).GetPersonArtistMBID(ctx, personID)
	if err != nil {
		return "", fmt.Errorf("library: artist id of person %d: %w", personID, err)
	}
	return mbid, nil
}
