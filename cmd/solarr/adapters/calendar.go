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

func (a Calendar) Entries(ctx context.Context, from, to string, limit int) ([]apiserver.CalendarRow, error) {
	entries, err := a.Calendar.Entries(ctx, from, to, limit)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.CalendarRow, len(entries))
	for i, entry := range entries {
		rows[i] = apiserver.CalendarRow{
			WorkID: entry.WorkID, Title: entry.Title, Artist: entry.Artist,
			PrimaryType: entry.PrimaryType, ReleaseDate: entry.ReleaseDate, OnDisk: entry.OnDisk,
		}
	}
	return rows, nil
}
