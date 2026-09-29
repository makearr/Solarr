package library

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// TagProviders names which provider's ids go into a file's tags, for work, edition, part and person.
type TagProviders struct {
	Work, Edition, Part, Person string
}

// TagCandidate is one file whose ids are known and were never written into it; empty ids are unknown.
type TagCandidate struct {
	MediaObjectID     int64
	RootFolderID      int64
	RelPath           string
	WorkID            int64
	WorkExternalID    string
	EditionExternalID string
	PartExternalID    string
	PersonExternalID  string
}

// TagWriteback records which files had their ids written into their tags (revision D3).
type TagWriteback struct{ store *store.Store }

// NewTagWriteback builds it over an open library store.
func NewTagWriteback(opened *store.Store) TagWriteback { return TagWriteback{store: opened} }

// Candidates answers up to limit unwritten files under rootFolderIDs, after afterID, by id.
func (t TagWriteback) Candidates(
	ctx context.Context, providers TagProviders, rootFolderIDs []int64, afterID int64, limit int,
) ([]TagCandidate, error) {
	roots, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, err
	}
	rows, err := librarydb.New(t.store.Read()).ListTagWritebackCandidates(ctx, librarydb.ListTagWritebackCandidatesParams{
		WorkProvider: providers.Work, EditionProvider: providers.Edition, PartProvider: providers.Part,
		PersonProvider: providers.Person, AfterID: afterID, RootFolderIds: string(roots), PageSize: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]TagCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = TagCandidate{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath, WorkID: row.WorkID,
			WorkExternalID: row.WorkExternalID, EditionExternalID: row.EditionExternalID,
			PartExternalID: row.PartExternalID, PersonExternalID: row.PersonExternalID,
		}
	}
	return candidates, nil
}

// Record notes one file as done: the keys written, or why it could not be.
func (t TagWriteback) Record(ctx context.Context, mediaObjectID, at int64, written, failure string) error {
	return t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordTagWriteback(ctx, librarydb.RecordTagWritebackParams{
			MediaObjectID: mediaObjectID, AttemptedAt: at, Written: written, Error: nullString(failure),
		})
	})
}
