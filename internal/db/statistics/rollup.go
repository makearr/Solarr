package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// Figure is one rollup row as it is written: a metric's value for one dimension, "<category id>|<key>".
type Figure struct {
	Metric, Dimension string
	Value             float64
	Count             int64
}

// CategoryFigure is one rollup row as one library reads it.
type CategoryFigure struct {
	Key        string
	Value      float64
	Count      int64
	ComputedAt int64
}

// Rollup is the statistics page's figures (section 21.12): a background job computes them, the page only reads them.
type Rollup struct{ store *store.Store }

// NewRollup builds it over an open statistics store.
func NewRollup(opened *store.Store) Rollup { return Rollup{store: opened} }

// Dimension is the dimension a library's key is stored under.
func Dimension(categoryID int64, key string) string {
	return strconv.FormatInt(categoryID, 10) + "|" + key
}

// Replace rebuilds the metrics named: their rows go, figures takes their place, in one transaction.
func (r Rollup) Replace(ctx context.Context, metrics []string, figures []Figure, computedAt int64) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		for _, metric := range metrics {
			if err := queries.DeleteStatRollupMetric(ctx, metric); err != nil {
				return err
			}
		}
		return setFigures(ctx, queries, figures, computedAt)
	})
	if err != nil {
		return fmt.Errorf("statistics: replace rollup: %w", err)
	}
	return nil
}

// ReplaceCategory rebuilds one library's rows of one metric.
func (r Rollup) ReplaceCategory(ctx context.Context, metric string, categoryID int64, figures []Figure, computedAt int64) error {
	err := r.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		if err := queries.DeleteStatRollupMetricForCategory(ctx, statisticsdb.DeleteStatRollupMetricForCategoryParams{
			Metric: metric, Prefix: Dimension(categoryID, ""),
		}); err != nil {
			return err
		}
		return setFigures(ctx, queries, figures, computedAt)
	})
	if err != nil {
		return fmt.Errorf("statistics: replace %s of library %d: %w", metric, categoryID, err)
	}
	return nil
}

func setFigures(ctx context.Context, queries *statisticsdb.Queries, figures []Figure, computedAt int64) error {
	for _, figure := range figures {
		if err := queries.SetStatRollup(ctx, statisticsdb.SetStatRollupParams{
			Metric: figure.Metric, Dimension: figure.Dimension, Period: 0, Value: figure.Value, Count: figure.Count, ComputedAt: computedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ForCategory answers a metric's current figures for one library; empty before the first rollup.
func (r Rollup) ForCategory(ctx context.Context, metric string, categoryID int64) ([]CategoryFigure, error) {
	prefix := Dimension(categoryID, "")
	rows, err := statisticsdb.New(r.store.Read()).ListStatRollupForCategory(ctx, statisticsdb.ListStatRollupForCategoryParams{
		Metric: metric, Prefix: prefix,
	})
	if err != nil {
		return nil, fmt.Errorf("statistics: %s: %w", metric, err)
	}
	figures := make([]CategoryFigure, len(rows))
	for i, row := range rows {
		figures[i] = CategoryFigure{Key: strings.TrimPrefix(row.Dimension, prefix), Value: row.Value, Count: row.Count, ComputedAt: row.ComputedAt}
	}
	return figures, nil
}

// Empty says whether the rollup holds nothing yet, as on the first start with statistics.db.
func (r Rollup) Empty(ctx context.Context) (bool, error) {
	count, err := statisticsdb.New(r.store.Read()).CountStatRollup(ctx)
	if err != nil {
		return false, fmt.Errorf("statistics: count rollup: %w", err)
	}
	return count == 0, nil
}

// StatDiscographyCoverage is the artists' coverage metric: key the person id, value the albums held, count the albums
// their discography has under the default filters.
const StatDiscographyCoverage = "discography_coverage"

// DiscographyCoverage is one artist's held albums against their discography.
type DiscographyCoverage struct {
	PersonID    int64
	Held, Total int64
}

// SetDiscographyCoverage replaces a library's coverage rows.
func (r Rollup) SetDiscographyCoverage(ctx context.Context, categoryID int64, rows []DiscographyCoverage, now int64) error {
	figures := make([]Figure, len(rows))
	for i, row := range rows {
		figures[i] = Figure{
			Metric: StatDiscographyCoverage, Dimension: Dimension(categoryID, strconv.FormatInt(row.PersonID, 10)),
			Value: float64(row.Held), Count: row.Total,
		}
	}
	return r.ReplaceCategory(ctx, StatDiscographyCoverage, categoryID, figures, now)
}

// IncompleteDiscographies lists a library's artists whose discography has albums not held, most missing first.
func (r Rollup) IncompleteDiscographies(ctx context.Context, categoryID int64, limit int) ([]DiscographyCoverage, error) {
	figures, err := r.ForCategory(ctx, StatDiscographyCoverage, categoryID)
	if err != nil {
		return nil, err
	}
	var rows []DiscographyCoverage
	for _, figure := range figures {
		personID, err := strconv.ParseInt(figure.Key, 10, 64)
		if err != nil || figure.Count <= int64(figure.Value) {
			continue
		}
		rows = append(rows, DiscographyCoverage{PersonID: personID, Held: int64(figure.Value), Total: figure.Count})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Total-rows[i].Held > rows[j].Total-rows[j].Held
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
