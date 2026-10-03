package library

import (
	"context"
	"encoding/json"
	"fmt"
)

// PictureSet is every provider picture a TV library shows, in the order a wall meets them.
type PictureSet struct {
	Posters, Logos, Photos []string
}

// PictureAddresses lists the poster of each series (the provider the page itself reads), the logo of each network and the
// photos of the people billed first in each series, at most maxPhotos of them.
func (r SeriesReader) PictureAddresses(ctx context.Context, maxPhotos int) (PictureSet, error) {
	var set PictureSet
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT f.work_id, f.value FROM work_field f JOIN work w ON w.id = f.work_id AND w.media_type = 'tv'
WHERE f.field = 'images'
ORDER BY f.work_id, CASE f.provider WHEN '__manual__' THEN 0 WHEN 'skyhook' THEN 1 WHEN 'tvmaze' THEN 2 ELSE 3 END`)
	if err != nil {
		return set, fmt.Errorf("library: poster addresses: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var last int64
	for rows.Next() {
		var workID int64
		var value string
		if err := rows.Scan(&workID, &value); err != nil {
			return set, fmt.Errorf("library: scan poster: %w", err)
		}
		if workID == last {
			continue
		}
		var images []struct{ Kind, URL string }
		if json.Unmarshal([]byte(value), &images) != nil {
			continue
		}
		for _, image := range images {
			if image.Kind == "poster" && image.URL != "" {
				set.Posters = append(set.Posters, image.URL)
				last = workID
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return set, fmt.Errorf("library: poster addresses: %w", err)
	}
	logos, err := r.store.Read().QueryContext(ctx,
		`SELECT DISTINCT json_extract(fields_json, '$.logo') FROM organisation WHERE kind = 'network' AND json_extract(fields_json, '$.logo') IS NOT NULL`)
	if err != nil {
		return set, fmt.Errorf("library: logo addresses: %w", err)
	}
	defer func() { _ = logos.Close() }()
	for logos.Next() {
		var address string
		if err := logos.Scan(&address); err == nil && address != "" {
			set.Logos = append(set.Logos, address)
		}
	}
	if err := logos.Err(); err != nil {
		return set, err
	}
	photos, err := r.store.Read().QueryContext(ctx,
		`SELECT DISTINCT json_extract(p.fields_json, '$.image_url')
FROM credit c JOIN person p ON p.id = c.person_id JOIN work w ON w.id = c.work_id AND w.media_type = 'tv'
WHERE c.part_id = 0 AND COALESCE(c.ord, 0) < 8 AND json_extract(p.fields_json, '$.image_url') IS NOT NULL
ORDER BY c.work_id DESC LIMIT ?`, maxPhotos)
	if err != nil {
		return set, fmt.Errorf("library: photo addresses: %w", err)
	}
	defer func() { _ = photos.Close() }()
	for photos.Next() {
		var address string
		if err := photos.Scan(&address); err == nil && address != "" {
			set.Photos = append(set.Photos, address)
		}
	}
	return set, photos.Err()
}
