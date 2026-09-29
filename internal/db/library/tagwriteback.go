package library

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// TagProviders names which provider's ids go into a file's tags, for work, edition, part and
// person, and which source's tags are its genres.
type TagProviders struct {
	Work, Edition, Part, Person, Genre string
}

// TagCandidate is one identified file with what its tags could take; empty values are unknown.
type TagCandidate struct {
	MediaObjectID     int64
	RootFolderID      int64
	RelPath           string
	WorkID            int64
	WorkTitle         string
	Year              int64
	ArtistName        string
	MediaRef          string
	PartTitle         string
	Genres            string
	CoverKey          string
	CoverFetchedAt    int64
	WorkExternalID    string
	EditionExternalID string
	PartExternalID    string
	PersonExternalID  string
	// Digest is what was last asked of this file, empty if never.
	Digest string
}

// TagWriteback records what the tag writer last asked of each file (revision D3).
type TagWriteback struct{ store *store.Store }

// NewTagWriteback builds it over an open library store.
func NewTagWriteback(opened *store.Store) TagWriteback { return TagWriteback{store: opened} }

// Candidates answers up to limit identified files under rootFolderIDs, after afterID, by id.
func (t TagWriteback) Candidates(
	ctx context.Context, providers TagProviders, rootFolderIDs []int64, afterID int64, limit int,
) ([]TagCandidate, error) {
	roots, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, err
	}
	rows, err := librarydb.New(t.store.Read()).ListTagWritebackCandidates(ctx, librarydb.ListTagWritebackCandidatesParams{
		GenreSource: providers.Genre, WorkProvider: providers.Work, EditionProvider: providers.Edition,
		PartProvider: providers.Part, PersonProvider: providers.Person, AfterID: afterID,
		RootFolderIds: string(roots), PageSize: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]TagCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = TagCandidate(row)
	}
	return candidates, nil
}

// Record notes what was asked of one file: its digest, the keys it changed, or why it failed.
func (t TagWriteback) Record(ctx context.Context, mediaObjectID, at int64, digest, written, failure string) error {
	return t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordTagWriteback(ctx, librarydb.RecordTagWritebackParams{
			MediaObjectID: mediaObjectID, AttemptedAt: at, Written: written, Error: nullString(failure), Digest: digest,
		})
	})
}
