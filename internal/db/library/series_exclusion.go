package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// SeriesExclusion is a series the user deleted and asked not to see again.
type SeriesExclusion struct {
	Provider, ExternalID, Title string
	CreatedAt                   int64
}

// Exclude remembers the series by every id it carries, so a scan, a follow or an import list finding it by any of them
// leaves it out.
func (s Series) Exclude(ctx context.Context, at time.Time, title string, ids []ExternalIDWrite) error {
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, id := range ids {
			if err := queries.AddSeriesExclusion(ctx, librarydb.AddSeriesExclusionParams{
				Provider: id.Provider, ExternalID: id.ExternalID, Title: title, CreatedAt: at.Unix(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: exclude series %q: %w", title, err)
	}
	return nil
}

// Excluded reports whether any of the ids belongs to an excluded series.
func (s Series) Excluded(ctx context.Context, ids []ExternalIDWrite) (bool, error) {
	queries := librarydb.New(s.store.Read())
	for _, id := range ids {
		count, err := queries.CountSeriesExclusion(ctx, librarydb.CountSeriesExclusionParams{Provider: id.Provider, ExternalID: id.ExternalID})
		if err != nil {
			return false, fmt.Errorf("library: excluded series: %w", err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Unexclude forgets the exclusion carried by any of the ids; the series may be added again.
func (s Series) Unexclude(ctx context.Context, ids []ExternalIDWrite) error {
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, id := range ids {
			if err := queries.DeleteSeriesExclusion(ctx, librarydb.DeleteSeriesExclusionParams{Provider: id.Provider, ExternalID: id.ExternalID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: unexclude series: %w", err)
	}
	return nil
}

// Exclusions lists the excluded series, by title; one series appears once per id it was excluded by.
func (s Series) Exclusions(ctx context.Context) ([]SeriesExclusion, error) {
	rows, err := librarydb.New(s.store.Read()).ListSeriesExclusions(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list series exclusions: %w", err)
	}
	out := make([]SeriesExclusion, len(rows))
	for i, row := range rows {
		out[i] = SeriesExclusion{Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, CreatedAt: row.CreatedAt}
	}
	return out, nil
}

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
