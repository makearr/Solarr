package library

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/listfilter"
)

// TrackFilter narrows the Tracks navigation level (ADR-54) the same way
// WorkFilter narrows Albums -- RootFolderIDs/CategoryIDs are ADR-27
// visibility's two halves, exactly WorkFilter's own fields.
type TrackFilter struct {
	RootFolderIDs []int64
	CategoryIDs   []int64
	// Descending orders the albums Z to A; tracks stay in their album's order.
	Descending bool
	// Search keeps the tracks whose title, album or artist contains it, any case; "" keeps all.
	Search string
	// Where is a custom filter over TrackFields, all of which must hold.
	Where []listfilter.Condition
}

// TrackFields are the fields a custom filter of the Tracks list may name.
var TrackFields = map[string]listfilter.Field{
	"title":     {Kind: listfilter.Text},
	"album":     {Kind: listfilter.Text},
	"artist":    {Kind: listfilter.Text},
	"year":      {Kind: listfilter.Number},
	"monitored": {Kind: listfilter.Bool},
	"has_file":  {Kind: listfilter.Bool},
}

const (
	trackArtistName = "COALESCE((SELECT p.name FROM person p WHERE p.id = work.lead_person_id), '')"
	trackHasFile    = "EXISTS (SELECT 1 FROM desired_item_file f JOIN desired_item d ON d.id = f.desired_item_id" +
		" JOIN library_path lp ON lp.media_object_id = f.media_object_id" +
		" WHERE d.work_id = wp.work_id AND f.media_ref = wp.media_ref AND f.role = 'primary' AND lp.missing_since IS NULL)"
	trackPartColumns = "wp.id, wp.work_id, wp.media_ref, wp.sort_key, wp.title, wp.runtime_ms, wp.date, wp.monitored," +
		" wp.available_at, wp.available_confidence, wp.available_basis, wp.available_grace"
)

// partPredicates are the filter's conditions on a track itself, over work_part wp joined to its work; empty when
// none narrows the tracks.
func (f TrackFilter) partPredicates() store.Predicates {
	var predicates store.Predicates
	if search := strings.TrimSpace(f.Search); search != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.ToLower(search)) + "%"
		predicates.Add("(LOWER(COALESCE(wp.title, '')) LIKE ? ESCAPE '\\' OR LOWER(work.canonical_title) LIKE ? ESCAPE '\\'"+
			" OR LOWER("+trackArtistName+") LIKE ? ESCAPE '\\')", like, like, like)
	}
	for _, condition := range f.Where {
		var fragment string
		var arguments []any
		switch condition.Field {
		case "title":
			fragment, arguments = condition.SQL(listfilter.Text, "COALESCE(wp.title, '')")
		case "album":
			fragment, arguments = condition.SQL(listfilter.Text, "work.canonical_title")
		case "artist":
			fragment, arguments = condition.SQL(listfilter.Text, trackArtistName)
		case "year":
			fragment, arguments = condition.SQL(listfilter.Number, "COALESCE(work.year, 0)")
		case "monitored":
			fragment, arguments = condition.SQL(listfilter.Bool, "wp.monitored = 1")
		case "has_file":
			fragment, arguments = condition.SQL(listfilter.Bool, trackHasFile)
		default:
			continue
		}
		predicates.Add(fragment, arguments...)
	}
	return predicates
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
	if parts := filter.partPredicates(); parts.Len() > 0 {
		predicates.Add("EXISTS (SELECT 1 FROM work_part wp WHERE wp.work_id = work.id AND "+parts.Joined()+")", parts.Args()...)
	}

	artistExpr := leadSortExpression
	operator, direction := ">", ""
	if filter.Descending {
		operator, direction = "<", " DESC"
	}
	predicates.AddIf(after.AlbumID != 0,
		"("+artistExpr+" "+operator+" "+leadSortCursor+" OR ("+artistExpr+" = "+leadSortCursor+" AND (sort_title "+operator+" ? OR (sort_title = ? AND id "+operator+" ?))))",
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
	narrowed := filter.partPredicates()
	for _, album := range albums {
		var parts []librarydb.WorkPart
		if narrowed.Len() == 0 {
			parts, err = queries.ListWorkParts(ctx, album.id)
		} else {
			parts, err = t.matchingParts(ctx, album.id, narrowed)
		}
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

// matchingParts answers an album's tracks that pass the filter, in the album's own order.
func (t TrackParts) matchingParts(ctx context.Context, albumID int64, narrowed store.Predicates) ([]librarydb.WorkPart, error) {
	query := "SELECT " + trackPartColumns + " FROM work_part wp JOIN work ON work.id = wp.work_id WHERE wp.work_id = ? AND " +
		narrowed.Joined() + " ORDER BY wp.sort_key, wp.id"
	rows, err := t.store.Read().QueryContext(ctx, query, append([]any{albumID}, narrowed.Args()...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var parts []librarydb.WorkPart
	for rows.Next() {
		var part librarydb.WorkPart
		if err := rows.Scan(&part.ID, &part.WorkID, &part.MediaRef, &part.SortKey, &part.Title, &part.RuntimeMs, &part.Date,
			&part.Monitored, &part.AvailableAt, &part.AvailableConfidence, &part.AvailableBasis, &part.AvailableGrace); err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, rows.Err()
}

// ExplainAlbumPage returns SQLite's plan for the album-page query List runs,
// so a test can assert the ordering actually reaches work_lead_person_sort
// rather than sorting the whole table (documentation/sql-and-sqlc.md, the
// same shape Works.ExplainList already gives the Albums level).
func (t TrackParts) ExplainAlbumPage(ctx context.Context, filter TrackFilter, after TrackAlbumCursor, albumPageSize int) (string, error) {
	query, arguments := composeAlbumPage(filter, after, albumPageSize)
	return t.store.ExplainQueryPlan(ctx, query, arguments...)
}
