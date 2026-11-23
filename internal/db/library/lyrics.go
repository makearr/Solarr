package library

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// LyricsCandidate is one identified file the lyrics pass may look up; empty values are unknown.
type LyricsCandidate struct {
	MediaObjectID int64
	RootFolderID  int64
	RelPath       string
	AlbumTitle    string
	ArtistName    string
	TrackTitle    string
	DurationMs    int64
}

// Lyrics records which files were asked about at the lyrics source.
type Lyrics struct{ store *store.Store }

// NewLyrics builds it over an open library store.
func NewLyrics(opened *store.Store) Lyrics { return Lyrics{store: opened} }

// Candidates answers up to limit files under rootFolderIDs after afterID, by id: never asked, or asked without luck before retryBefore.
func (l Lyrics) Candidates(ctx context.Context, rootFolderIDs []int64, afterID, retryBefore int64, limit int) ([]LyricsCandidate, error) {
	roots, err := json.Marshal(rootFolderIDs)
	if err != nil {
		return nil, err
	}
	rows, err := librarydb.New(l.store.Read()).ListLyricsCandidates(ctx, librarydb.ListLyricsCandidatesParams{
		AfterID: afterID, RootFolderIds: string(roots), RetryBefore: retryBefore, PageSize: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	candidates := make([]LyricsCandidate, len(rows))
	for i, row := range rows {
		candidates[i] = LyricsCandidate(row)
	}
	return candidates, nil
}

// Record notes that the file was asked about, and whether lyrics came back.
func (l Lyrics) Record(ctx context.Context, mediaObjectID, at int64, found bool) error {
	return l.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordLyricsAttempt(ctx, librarydb.RecordLyricsAttemptParams{
			MediaObjectID: mediaObjectID, AttemptedAt: at, Found: boolToInt(found),
		})
	})
}
