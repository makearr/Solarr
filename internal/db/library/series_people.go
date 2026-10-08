package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ErrPersonNotFound reports a person with no credit in the library asked about.
var ErrPersonNotFound = errors.New("library: person not found")

func imageFromFields(raw sql.NullString) string {
	if !raw.Valid {
		return ""
	}
	var fields personFields
	if json.Unmarshal([]byte(raw.String), &fields) != nil {
		return ""
	}
	return fields.ImageURL
}

// TVNetworkRow is a network or streamer with how many of the library's series air on it.
type TVNetworkRow struct {
	ID          int64
	Name        string
	Country     string
	LogoURL     string
	SeriesCount int64
}

// SummariesByIDs lists the given series of a category as a list shows them, in sort-title order.
func (r SeriesReader) SummariesByIDs(ctx context.Context, categoryID int64, ids []int64, now time.Time) ([]SeriesSummary, error) {
	queries := librarydb.New(r.store.Read())
	var summaries []SeriesSummary
	for _, id := range ids {
		row, err := queries.GetSeriesInCategory(ctx, librarydb.GetSeriesInCategoryParams{ID: id, CategoryID: categoryID})
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("library: series %d: %w", id, err)
		}
		summaries = append(summaries, SeriesSummary{
			WorkID: row.ID, Title: row.CanonicalTitle, SortTitle: row.SortTitle, Year: row.Year.Int64,
			Description: row.Description.String, AddedAt: row.AddedAt,
		})
	}
	kept := make([]int64, len(summaries))
	for i, summary := range summaries {
		kept[i] = summary.WorkID
	}
	if err := r.fill(ctx, queries, summaries, kept, now); err != nil {
		return nil, err
	}
	return summaries, nil
}

// NetworkOfSeries answers the network a series airs on, with its logo; found is false for a series with none.
func (r SeriesReader) NetworkOfSeries(ctx context.Context, workID int64) (TVNetworkRow, bool, error) {
	var row TVNetworkRow
	err := r.store.Read().QueryRowContext(ctx,
		`SELECT o.id, o.name, COALESCE(o.country, ''), COALESCE(json_extract(o.fields_json, '$.logo'), '')
FROM work_organisation wo JOIN organisation o ON o.id = wo.organisation_id
WHERE wo.work_id = ? AND wo.role = 'network' AND o.kind = 'network'
ORDER BY o.id LIMIT 1`, workID).Scan(&row.ID, &row.Name, &row.Country, &row.LogoURL)
	if errors.Is(err, sql.ErrNoRows) {
		return TVNetworkRow{}, false, nil
	}
	if err != nil {
		return TVNetworkRow{}, false, fmt.Errorf("library: series network: %w", err)
	}
	return row, true, nil
}
