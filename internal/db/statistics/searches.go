package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// RecordSearch logs one search of a wanted item and whether it found a release the library accepts.
func (c Candidates) RecordSearch(ctx context.Context, categoryID, itemID int64, found bool, at time.Time) error {
	err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return statisticsdb.New(tx).InsertSearchLog(ctx, statisticsdb.InsertSearchLogParams{
			At: at.Unix(), CategoryID: categoryID, DesiredItemID: itemID, Found: boolInt(found),
		})
	})
	if err != nil {
		return fmt.Errorf("statistics: record search: %w", err)
	}
	return nil
}

// SearchesByCategory counts the searches since a time and how many found something, per library, for the statistics.
func (c Candidates) SearchesByCategory(ctx context.Context, since int64) ([]Figure, error) {
	rows, err := statisticsdb.New(c.store.Read()).AggregateSearchesByCategory(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("statistics: searches: %w", err)
	}
	figures := make([]Figure, 0, 2*len(rows))
	for _, row := range rows {
		figures = append(figures,
			Figure{Metric: library.StatSearches, Dimension: Dimension(row.CategoryID, library.SearchesKeySearched), Value: float64(row.Searches), Count: row.Searches},
			Figure{Metric: library.StatSearches, Dimension: Dimension(row.CategoryID, library.SearchesKeyFound), Value: float64(row.Found), Count: row.Found},
		)
	}
	return figures, nil
}

// retireSearches deletes searches logged before a time, a page per transaction, at most maxPages.
func (c Candidates) retireSearches(ctx context.Context, before time.Time, maxPages int) (int64, error) {
	var total int64
	for range maxPages {
		var removed int64
		err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			removed, err = statisticsdb.New(tx).DeleteSearchLogBefore(ctx, statisticsdb.DeleteSearchLogBeforeParams{
				Before: before.Unix(), PageSize: retentionPage,
			})
			return err
		})
		if err != nil {
			return total, fmt.Errorf("statistics: retire searches: %w", err)
		}
		total += removed
		if removed < retentionPage {
			break
		}
	}
	return total, nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
