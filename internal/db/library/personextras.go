package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
)

// PersonFact is one labelled fact on a person's page ("Height", "172 cm"), in the order the media type gives them.
type PersonFact struct{ Label, Value string }

// PersonWebLink is an address of the person: a profile, a home page.
type PersonWebLink struct{ Label, URL string }

// PersonExtras is what a media type knows of a person beyond the columns: facts, links and further pictures. It is kept in
// fields_json, so core stores and serves it without knowing what the facts mean (ADR-23).
type PersonExtras struct {
	Facts  []PersonFact
	Links  []PersonWebLink
	Images []string
}

type extrasFields struct {
	Facts  []extraFact `json:"facts,omitempty"`
	Links  []extraLink `json:"links,omitempty"`
	Images []string    `json:"images,omitempty"`
}

type extraFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type extraLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// SetExtras replaces a person's facts, links and pictures; the rest of fields_json stays.
func (p People) SetExtras(ctx context.Context, personID int64, extras PersonExtras) error {
	encoded := extrasFields{Images: extras.Images}
	for _, fact := range extras.Facts {
		encoded.Facts = append(encoded.Facts, extraFact(fact))
	}
	for _, link := range extras.Links {
		encoded.Links = append(encoded.Links, extraLink(link))
	}
	facts, err := json.Marshal(encoded.Facts)
	if err != nil {
		return err
	}
	links, err := json.Marshal(encoded.Links)
	if err != nil {
		return err
	}
	images, err := json.Marshal(encoded.Images)
	if err != nil {
		return err
	}
	err = p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE person SET fields_json = json_set(COALESCE(NULLIF(fields_json, ''), '{}'), '$.facts', json(?), '$.links', json(?), '$.images', json(?)) WHERE id = ?`,
			string(facts), string(links), string(images), personID)
		return err
	})
	if err != nil {
		return fmt.Errorf("library: person %d extras: %w", personID, err)
	}
	return nil
}

// extrasFromFields decodes the extras out of fields_json, empty when there are none.
func extrasFromFields(raw sql.NullString) PersonExtras {
	var extras PersonExtras
	if !raw.Valid {
		return extras
	}
	var fields extrasFields
	if json.Unmarshal([]byte(raw.String), &fields) != nil {
		return extras
	}
	for _, fact := range fields.Facts {
		extras.Facts = append(extras.Facts, PersonFact(fact))
	}
	for _, link := range fields.Links {
		extras.Links = append(extras.Links, PersonWebLink(link))
	}
	extras.Images = fields.Images
	return extras
}

// DueForDetails answers up to limit people credited in the role on a work of the media type whose details were not read
// since before, never-read first.
func (p People) DueForDetails(ctx context.Context, mediaType, role string, before time.Time, limit int) ([]int64, error) {
	rows, err := p.store.Read().QueryContext(ctx, `SELECT DISTINCT person.id, COALESCE(r.refreshed_at, 0) AS refreshed
FROM person
JOIN credit c ON c.person_id = person.id AND c.role = ?
JOIN work ON work.id = c.work_id AND work.media_type = ?
LEFT JOIN person_refresh r ON r.person_id = person.id
WHERE COALESCE(r.refreshed_at, 0) < ?
ORDER BY refreshed, person.id LIMIT ?`, role, mediaType, before.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("library: people due details: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id, refreshed int64
		if err := rows.Scan(&id, &refreshed); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
