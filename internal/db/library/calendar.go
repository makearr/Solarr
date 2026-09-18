package library

import (
	"context"
	"fmt"

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
func (c Calendar) Entries(ctx context.Context, from, to string, pageSize int) ([]CalendarEntry, error) {
	rows, err := librarydb.New(c.store.Read()).ListCalendarEntries(ctx, librarydb.ListCalendarEntriesParams{
		Kind: WorkDateStreet, FromDate: from, ToDate: to, PageSize: int64(pageSize),
	})
	if err != nil {
		return nil, fmt.Errorf("library: calendar entries %s..%s: %w", from, to, err)
	}
	entries := make([]CalendarEntry, len(rows))
	for i, row := range rows {
		entries[i] = CalendarEntry{
			WorkID: row.ID, Title: row.CanonicalTitle, Artist: row.ArtistSort,
			PrimaryType: row.PrimaryType.String, ReleaseDate: asString(row.ReleaseDate),
			OnDisk: row.OnDisk,
		}
	}
	return entries, nil
}

// asString normalises MIN()'s own any-typed result: sqlc types an aggregate as
// interface{}, and the driver hands back a string for a TEXT column.
func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
