package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// Calendar satisfies apiserver.CalendarReader over library.Calendar.
type Calendar struct {
	Calendar library.Calendar
}

func (a Calendar) Entries(
	ctx context.Context, from, to string, categoryIDs []int64, limit int,
) ([]apiserver.CalendarRow, error) {
	entries, err := a.Calendar.Entries(ctx, from, to, categoryIDs, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.CalendarRow, len(entries))
	for i, entry := range entries {
		rows[i] = apiserver.CalendarRow{
			WorkID: entry.WorkID, Title: entry.Title, Artist: entry.Artist,
			PrimaryType: entry.PrimaryType, ReleaseDate: entry.ReleaseDate, OnDisk: entry.OnDisk,
			PartID: entry.PartID, CategoryID: entry.CategoryID,
		}
	}
	return rows, nil
}

// Dates satisfies apiserver.CalendarReader's per-date count.
func (a Calendar) Dates(ctx context.Context, from, to string, categoryIDs []int64) ([]apiserver.CalendarDate, error) {
	dates, err := a.Calendar.Dates(ctx, from, to, categoryIDs)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.CalendarDate, len(dates))
	for i, date := range dates {
		rows[i] = apiserver.CalendarDate{Date: date.Date, Releases: date.Releases}
	}
	return rows, nil
}
