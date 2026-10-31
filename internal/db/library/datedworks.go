package library

import (
	"context"
	"fmt"
)

// DatedWork is a work of a library with its release day and one field of it, for matching a release a feed lists.
type DatedWork struct {
	WorkID int64
	Title  string
	// Date is the release day, yyyy-mm-dd.
	Date string
	// Field is the value of the field asked for, empty when the work has none.
	Field string
}

// DatedOf answers the works of the media type held in a library that have a full release day, with the value of field.
func (r SeriesReader) DatedOf(ctx context.Context, mediaType string, categoryID int64, field string) ([]DatedWork, error) {
	rows, err := r.store.Read().QueryContext(ctx, `SELECT w.id, w.canonical_title, d.date,
       COALESCE((SELECT f.value FROM work_field f WHERE f.work_id = w.id AND f.field = ? ORDER BY (f.provider = 'manual') DESC LIMIT 1), '')
FROM work w
JOIN work_date d ON d.work_id = w.id AND d.kind = 'release' AND length(d.date) = 10
WHERE w.media_type = ? AND EXISTS (SELECT 1 FROM desired_item i WHERE i.work_id = w.id AND i.category_id = ?)`, field, mediaType, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: dated works: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var works []DatedWork
	for rows.Next() {
		var work DatedWork
		if err := rows.Scan(&work.WorkID, &work.Title, &work.Date, &work.Field); err != nil {
			return nil, err
		}
		works = append(works, work)
	}
	return works, rows.Err()
}
