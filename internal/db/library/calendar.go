package library

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// CalendarEntry is one release in a date window.
type CalendarEntry struct {
	WorkID int64
	Title  string
	Artist string
	// PrimaryType is MusicBrainz's own vocabulary ("Album", "Single", ...),
	// empty when the work has none yet.
	PrimaryType string
	// ReleaseDate is a partial ISO date as the provider gave it: "2024",
	// "2024-03" or "2024-03-01". Shown as-is rather than guessed into a full
	// date, since a month-precision release is a real thing to say.
	ReleaseDate string
	OnDisk      bool
	// PartID is the episode, zero for a release; CategoryID is the TV library an episode is in.
	PartID     int64
	CategoryID int64
}

// Calendar reads upcoming and recent releases.
type Calendar struct {
	store *store.Store
}

// NewCalendar builds one over an open library store.
func NewCalendar(opened *store.Store) Calendar { return Calendar{store: opened} }

// Entries answers releases between from and to inclusive, earliest first, for
// works this library has a reason to care about: something wants the work, or
// an artist credited on it is monitored. Dates are compared as ISO strings,
// which sorts correctly and lets a partial date sit in the window it belongs
// to without inventing a day for it.
func (c Calendar) Entries(
	ctx context.Context, from, to string, categoryIDs []int64, pageSize int,
) ([]CalendarEntry, error) {
	encoded, err := json.Marshal(categoryIDs)
	if err != nil {
		return nil, fmt.Errorf("library: calendar categories: %w", err)
	}
	rows, err := librarydb.New(c.store.Read()).ListCalendarEntries(ctx, librarydb.ListCalendarEntriesParams{
		Kind: WorkDateStreet, FromDate: from, ToDate: to, CategoryIds: string(encoded), PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("library: calendar entries %s..%s: %w", from, to, err)
	}
	entries := make([]CalendarEntry, len(rows))
	for i, row := range rows {
		entries[i] = CalendarEntry{
			WorkID: row.ID, Title: row.CanonicalTitle, Artist: row.ArtistName,
			PrimaryType: row.PrimaryType.String, ReleaseDate: asString(row.ReleaseDate),
			OnDisk: row.OnDisk,
		}
	}
	episodes, err := librarydb.New(c.store.Read()).ListCalendarEpisodes(ctx, librarydb.ListCalendarEpisodesParams{
		CategoryIds: string(encoded), FromDate: sql.NullString{String: from, Valid: true}, ToDate: sql.NullString{String: to, Valid: true}, PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("library: calendar episodes %s..%s: %w", from, to, err)
	}
	for _, row := range episodes {
		season, number := parseSeriesRef(row.MediaRef)
		entries = append(entries, CalendarEntry{
			WorkID: row.WorkID, Title: row.CanonicalTitle, Artist: fmt.Sprintf("S%02dE%02d %s", season, number, row.Title.String),
			PrimaryType: "Episode", ReleaseDate: row.Date.String, OnDisk: row.OnDisk, PartID: row.PartID, CategoryID: asInt(row.CategoryID),
		})
	}
	if len(episodes) > 0 {
		slices.SortStableFunc(entries, func(a, b CalendarEntry) int { return cmp.Compare(a.ReleaseDate, b.ReleaseDate) })
	}
	return entries, nil
}

func asInt(value any) int64 {
	switch number := value.(type) {
	case int64:
		return number
	case float64:
		return int64(number)
	}
	return 0
}

// asString normalises MIN()'s own any-typed result: sqlc types an aggregate as
// interface{}, and the driver hands back a string for a TEXT column.
func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

// CalendarDate is how many releases share one date, as precise as the provider gave it.
type CalendarDate struct {
	Date     string
	Releases int64
}

// Dates counts Entries' releases per date, for a view that needs every dot but no titles.
func (c Calendar) Dates(ctx context.Context, from, to string, categoryIDs []int64) ([]CalendarDate, error) {
	encoded, err := json.Marshal(categoryIDs)
	if err != nil {
		return nil, fmt.Errorf("library: calendar categories: %w", err)
	}
	rows, err := librarydb.New(c.store.Read()).CountCalendarDates(ctx, librarydb.CountCalendarDatesParams{
		Kind: WorkDateStreet, FromDate: from, ToDate: to, CategoryIds: string(encoded),
	})
	if err != nil {
		return nil, fmt.Errorf("library: calendar dates %s..%s: %w", from, to, err)
	}
	byDate := map[string]int64{}
	for _, row := range rows {
		byDate[asString(row.ReleaseDate)] += row.Releases
	}
	episodeRows, err := librarydb.New(c.store.Read()).CountCalendarEpisodeDates(ctx, librarydb.CountCalendarEpisodeDatesParams{
		CategoryIds: string(encoded), FromDate: sql.NullString{String: from, Valid: true}, ToDate: sql.NullString{String: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("library: calendar episode dates %s..%s: %w", from, to, err)
	}
	for _, row := range episodeRows {
		byDate[row.ReleaseDate.String] += row.Releases
	}
	dates := make([]CalendarDate, 0, len(byDate))
	for date, releases := range byDate {
		dates = append(dates, CalendarDate{Date: date, Releases: releases})
	}
	slices.SortFunc(dates, func(a, b CalendarDate) int { return cmp.Compare(a.Date, b.Date) })
	return dates, nil
}
