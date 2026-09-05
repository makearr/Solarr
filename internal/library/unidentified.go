package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/paginate"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// Unidentified is F29's manual identification queue: what a scan recorded that the
// ADR-24 chain (model.RunChain) could not resolve with enough confidence to link
// automatically.
type Unidentified struct{ store *store.Store }

// NewUnidentified builds it over an open library store.
func NewUnidentified(opened *store.Store) Unidentified { return Unidentified{store: opened} }

// QueueEntry is one file to record or refresh in the queue.
type QueueEntry struct {
	MediaObjectID int64
	RootFolderID  int64
	RelPath       string
	// Candidates is RunChain's merged result, best first -- empty when nothing
	// matched at all, non-empty when something did but not confidently enough
	// (ChainResult.Best was nil either way).
	Candidates []model.Identification
	At         int64
}

// Candidate is the queue's own, deliberately narrower shape for one ranked
// suggestion: WorkID is always nil for a queued entry (RunChain never sets Best
// from a candidate that did not clear AmbiguityThreshold) and Evidence is not
// shown here -- the full page (§21.3) is where that depth belongs.
type Candidate struct {
	Strategy   string  `json:"strategy"`
	Provider   string  `json:"provider,omitempty"`
	ExternalID string  `json:"external_id,omitempty"`
	Confidence float64 `json:"confidence"`
}

// Row is one page entry.
type Row struct {
	MediaObjectID int64
	RootFolderID  int64
	RelPath       string
	Candidates    []Candidate
	FirstSeenAt   int64
	LastSeenAt    int64
}

func encodeCandidates(candidates []model.Identification) (string, error) {
	encoded := make([]Candidate, len(candidates))
	for index, candidate := range candidates {
		entry := Candidate{Strategy: candidate.Strategy, Confidence: candidate.Confidence}
		if candidate.ExternalID != nil {
			entry.Provider = candidate.ExternalID.Provider
			entry.ExternalID = candidate.ExternalID.ExternalID
		}
		encoded[index] = entry
	}
	raw, err := json.Marshal(encoded)
	if err != nil {
		return "", fmt.Errorf("library: encode candidates: %w", err)
	}
	return string(raw), nil
}

// Record upserts a batch of unresolved files in one transaction -- the same bound
// a scan/consumer batch already commits at (Invariant 2), so this never opens a
// second write beyond what Physical.RegisterBatch already does per batch.
func (u Unidentified) Record(ctx context.Context, entries []QueueEntry) error {
	if len(entries) == 0 {
		return nil
	}
	return u.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, entry := range entries {
			candidatesJSON, err := encodeCandidates(entry.Candidates)
			if err != nil {
				return err
			}
			if err := queries.UpsertUnidentified(ctx, librarydb.UpsertUnidentifiedParams{
				MediaObjectID: entry.MediaObjectID, RootFolderID: entry.RootFolderID,
				RelPath: entry.RelPath, CandidatesJson: candidatesJSON,
				FirstSeenAt: entry.At, LastSeenAt: entry.At,
			}); err != nil {
				return fmt.Errorf("upsert %s: %w", entry.RelPath, err)
			}
		}
		return nil
	})
}

// Get answers one queued entry, for the manual resolve action -- confirming a
// candidate needs the file's own RootFolderID (to link work_root_folder),
// which List's page rows already carry but a caller resolving a single id
// from a request path should not have to page through the whole queue for.
// A missing id surfaces sql.ErrNoRows, wrapped: callers use errors.Is.
func (u Unidentified) Get(ctx context.Context, mediaObjectID int64) (Row, error) {
	row, err := librarydb.New(u.store.Read()).GetUnidentified(ctx, mediaObjectID)
	if err != nil {
		return Row{}, fmt.Errorf("library: get unidentified %d: %w", mediaObjectID, err)
	}
	var candidates []Candidate
	if err := json.Unmarshal([]byte(row.CandidatesJson), &candidates); err != nil {
		return Row{}, fmt.Errorf("library: decode candidates for media object %d: %w", mediaObjectID, err)
	}
	return Row{
		MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
		Candidates: candidates, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
	}, nil
}

// Clear removes entries once they resolve -- a later sweep identifying a file that
// was previously queued. Deleting a media_object_id not currently queued is a
// silent no-op, the same as any idempotent repair path in this codebase.
func (u Unidentified) Clear(ctx context.Context, mediaObjectIDs []int64) error {
	if len(mediaObjectIDs) == 0 {
		return nil
	}
	return u.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, id := range mediaObjectIDs {
			if err := queries.DeleteUnidentified(ctx, id); err != nil {
				return fmt.Errorf("clear %d: %w", id, err)
			}
		}
		return nil
	})
}

// List returns one page, oldest-queued first. rootFolderIDs follows WorkFilter's
// own convention (search.go, works.go): nil is unfiltered, a non-nil empty slice
// matches nothing (ADR-27 -- the caller translates "no root folder visible" into
// that before calling, rather than the two being indistinguishable here).
func (u Unidentified) List(ctx context.Context, rootFolderIDs []int64, after paginate.Cursor, pageSize int) ([]Row, error) {
	filterRootFolders := rootFolderIDs != nil
	encodedRootFolderIDs, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, fmt.Errorf("library: encode root folder ids: %w", err)
	}

	rows, err := librarydb.New(u.store.Read()).ListUnidentified(ctx, librarydb.ListUnidentifiedParams{
		FilterRootFolders: boolToInt(filterRootFolders), RootFolderIds: string(encodedRootFolderIDs),
		AfterSeen: after.Sort, AfterID: after.ID, PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("library: list unidentified: %w", err)
	}

	views := make([]Row, len(rows))
	for index, row := range rows {
		var candidates []Candidate
		if err := json.Unmarshal([]byte(row.CandidatesJson), &candidates); err != nil {
			return nil, fmt.Errorf("library: decode candidates for media object %d: %w", row.MediaObjectID, err)
		}
		views[index] = Row{
			MediaObjectID: row.MediaObjectID, RootFolderID: row.RootFolderID, RelPath: row.RelPath,
			Candidates: candidates, FirstSeenAt: row.FirstSeenAt, LastSeenAt: row.LastSeenAt,
		}
	}
	return views, nil
}
