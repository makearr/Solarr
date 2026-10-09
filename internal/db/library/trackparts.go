package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// TrackFilter narrows the Tracks navigation level (ADR-54) the same way
// WorkFilter narrows Albums -- RootFolderIDs/CategoryIDs are ADR-27
// visibility's two halves, exactly WorkFilter's own fields.
type TrackFilter struct {
	RootFolderIDs []int64
	CategoryIDs   []int64
	// Descending orders the albums Z to A; tracks stay in their album's order.
	Descending bool
}

// TrackAlbumCursor is a position among the albums backing a Tracks page (see
// TrackParts.List's own doc comment for why Tracks paginates at the album
// boundary rather than the track).
type TrackAlbumCursor struct {
	ArtistSortName string
	AlbumSortTitle string
	AlbumID        int64
}

// TrackRow is one work_part with its parent album's display fields -- what
// the Tracks level actually shows a row of. AlbumWorkID is the album's own
// work id, not the track's (WorkPart.ID is the track's own work_part id) --
// what a caller needs to point a track's own cover art at its album's
// (the song-falls-back-to-album-image chain, 2026-09-11).
type TrackRow struct {
	librarydb.WorkPart
	AlbumWorkID int64
	AlbumTitle  string
	AlbumYear   sql.NullInt64
	ArtistName  sql.NullString
}

// trackAlbum is one album in the page, before its parts are fetched.
type trackAlbum struct {
	id             int64
	sortTitle      string
	title          string
	year           sql.NullInt64
	artistSortName sql.NullString
	artistName     sql.NullString
}

// TrackParts reads the Tracks navigation level: work_part rows flattened
// across every album visible to a category.
type TrackParts struct{ store *store.Store }

// NewTrackParts builds a reader over an open library store.
func NewTrackParts(opened *store.Store) TrackParts { return TrackParts{store: opened} }

// composeAlbumPage builds the album-page query backing List, ordered by the
// work_lead_person_sort index (migration 0021) rather than an unindexed
// per-row credit/person subquery (ADR-28).
func composeAlbumPage(filter TrackFilter, after TrackAlbumCursor, albumPageSize int) (string, []any) {
	var predicates store.Predicates
	addVisibility(&predicates, filter.RootFolderIDs, filter.CategoryIDs)

	// Matches the expression index (migration 0046) exactly; a work with no artist sorts first.
	artistExpr := "COALESCE(lead_person_sort_name, '') COLLATE NOCASE"
	operator, direction := ">", ""
	if filter.Descending {
		operator, direction = "<", " DESC"
	}
	predicates.AddIf(after.AlbumID != 0,
		"("+artistExpr+" "+operator+" ? OR ("+artistExpr+" = ? AND (sort_title "+operator+" ? OR (sort_title = ? AND id "+operator+" ?))))",
		after.ArtistSortName, after.ArtistSortName, after.AlbumSortTitle, after.AlbumSortTitle, after.AlbumID)

	query := "SELECT id, sort_title, canonical_title, year, lead_person_sort_name,\n" +
		"       (SELECT p.name FROM person p WHERE p.id = work.lead_person_id) AS artist_name\n" +
		"FROM work" + predicates.Where() +
		"\nORDER BY " + artistExpr + direction + ", sort_title" + direction + ", id" + direction + "\nLIMIT ?"
	return query, predicates.Args(albumPageSize)
}

// List returns one page of the Tracks level: pageSize ALBUMS worth of parts,
// not pageSize tracks, so one album's parts are never split across two pages
// -- the page's actual track count varies with how long those albums are.
func (t TrackParts) List(
	ctx context.Context, filter TrackFilter, after TrackAlbumCursor, albumPageSize int,
) (rows []TrackRow, nextCursor TrackAlbumCursor, hasMore bool, err error) {
	query, arguments := composeAlbumPage(filter, after, albumPageSize+1)

	albumRows, err := t.store.Read().QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, TrackAlbumCursor{}, false, fmt.Errorf("library: list track albums: %w", err)
	}
	defer func() { _ = albumRows.Close() }()

	var albums []trackAlbum
	for albumRows.Next() {
		var album trackAlbum
		if err := albumRows.Scan(&album.id, &album.sortTitle, &album.title, &album.year,
			&album.artistSortName, &album.artistName); err != nil {
			return nil, TrackAlbumCursor{}, false, fmt.Errorf("library: scan track album: %w", err)
		}
		albums = append(albums, album)
	}
	if err := albumRows.Err(); err != nil {
		return nil, TrackAlbumCursor{}, false, fmt.Errorf("library: list track albums: %w", err)
	}

	if len(albums) > albumPageSize {
		hasMore = true
		albums = albums[:albumPageSize]
	}
	if len(albums) > 0 {
		last := albums[len(albums)-1]
		nextCursor = TrackAlbumCursor{
			ArtistSortName: last.artistSortName.String, AlbumSortTitle: last.sortTitle, AlbumID: last.id,
		}
	}

	queries := librarydb.New(t.store.Read())
	for _, album := range albums {
		parts, err := queries.ListWorkParts(ctx, album.id)
		if err != nil {
			return nil, TrackAlbumCursor{}, false, fmt.Errorf("library: list parts for work %d: %w", album.id, err)
		}
		for _, part := range parts {
			rows = append(rows, TrackRow{
				WorkPart: part, AlbumWorkID: album.id, AlbumTitle: album.title,
				AlbumYear: album.year, ArtistName: album.artistName,
			})
		}
	}
	return rows, nextCursor, hasMore, nil
}

// ExplainAlbumPage returns SQLite's plan for the album-page query List runs,
// so a test can assert the ordering actually reaches work_lead_person_sort
// rather than sorting the whole table (documentation/sql-and-sqlc.md, the
// same shape Works.ExplainList already gives the Albums level).
func (t TrackParts) ExplainAlbumPage(ctx context.Context, filter TrackFilter, after TrackAlbumCursor, albumPageSize int) (string, error) {
	query, arguments := composeAlbumPage(filter, after, albumPageSize)
	return t.store.ExplainQueryPlan(ctx, query, arguments...)
}
