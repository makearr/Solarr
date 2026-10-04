package library

import (
	"context"
	"encoding/json"
	"fmt"
)

// SeriesImage is one picture of a series that a provider (or the user) holds.
type SeriesImage struct {
	Kind, URL, Source string
}

// Images lists every picture each provider holds of a series, the user's own and the best provider's first, a
// repeated address once.
func (r SeriesReader) Images(ctx context.Context, workID int64) ([]SeriesImage, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT provider, value FROM work_field WHERE work_id = ? AND field = 'images'
ORDER BY CASE provider WHEN '__manual__' THEN 0 WHEN 'skyhook' THEN 1 WHEN 'tvmaze' THEN 2 ELSE 3 END, provider`, workID)
	if err != nil {
		return nil, fmt.Errorf("library: series images: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var images []SeriesImage
	seen := map[string]bool{}
	for rows.Next() {
		var provider, value string
		if err := rows.Scan(&provider, &value); err != nil {
			return nil, fmt.Errorf("library: scan series images: %w", err)
		}
		var stored []struct{ Kind, URL, Source string }
		if json.Unmarshal([]byte(value), &stored) != nil {
			continue
		}
		for _, image := range stored {
			if image.URL == "" || seen[image.Kind+"\x00"+image.URL] {
				continue
			}
			seen[image.Kind+"\x00"+image.URL] = true
			source := image.Source
			if source == "" {
				source = provider
			}
			images = append(images, SeriesImage{Kind: image.Kind, URL: image.URL, Source: source})
		}
	}
	return images, rows.Err()
}

// HeldTMDB maps the TMDB id of each series a category holds to its work, so a title another source names can be
// recognised as one already in the library.
func (r SeriesReader) HeldTMDB(ctx context.Context, categoryID int64) (map[int64]int64, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT e.external_id, e.work_id FROM work_external_id e JOIN work w ON w.id = e.work_id
WHERE e.provider = 'tmdb' AND w.media_type = 'tv'
  AND EXISTS (SELECT 1 FROM desired_item d WHERE d.work_id = w.id AND d.category_id = ?)`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: held tmdb ids: %w", err)
	}
	defer func() { _ = rows.Close() }()
	held := map[int64]int64{}
	for rows.Next() {
		var external string
		var workID int64
		if err := rows.Scan(&external, &workID); err != nil {
			return nil, fmt.Errorf("library: scan held tmdb id: %w", err)
		}
		var id int64
		if _, err := fmt.Sscan(external, &id); err == nil {
			held[id] = workID
		}
	}
	return held, rows.Err()
}
