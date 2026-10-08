package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// MediaObjects lists every file a series holds, whichever way it is linked.
func (r SeriesReader) MediaObjects(ctx context.Context, workID int64) ([]int64, error) {
	ids, err := librarydb.New(r.store.Read()).ListSeriesMediaObjects(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: series files: %w", err)
	}
	return ids, nil
}

// DeleteSeries removes a series with its episodes, credits, desired items and links to files, whether or not it still
// has files: with the series excluded a scan no longer brings it back, and the files are the caller's to have recycled.
func (s Series) DeleteSeries(ctx context.Context, workID int64) error {
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return deleteFilelessWork(ctx, librarydb.New(tx), workID)
	})
	if err != nil {
		return fmt.Errorf("library: delete series %d: %w", workID, err)
	}
	return nil
}

// SetManualField gives a series field a value of the user's own, which every provider's value gives way to; an empty
// value returns the field to the providers'.
func (s Series) SetManualField(ctx context.Context, at time.Time, workID int64, field, value string) error {
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if value == "" {
			return queries.DeleteWorkFieldByProvider(ctx, librarydb.DeleteWorkFieldByProviderParams{WorkID: workID, Field: field, Provider: "__manual__"})
		}
		return queries.SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: workID, Field: field, Provider: "__manual__", Value: sql.NullString{String: value, Valid: true}, FetchedAt: at.Unix(),
		})
	})
	if err != nil {
		return fmt.Errorf("library: set %s of series %d: %w", field, workID, err)
	}
	return nil
}
