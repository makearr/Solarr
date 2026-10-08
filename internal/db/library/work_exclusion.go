package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Exclusions are the works the user removed and asked not to see again, of any media type.
type Exclusions struct{ store *store.Store }

// NewExclusions wraps an opened library database.
func NewExclusions(opened *store.Store) Exclusions { return Exclusions{store: opened} }

// WorkExclusion is one excluded work, by one of its ids.
type WorkExclusion struct {
	MediaType, Provider, ExternalID, Title string
	CreatedAt                              int64
}

// Exclude remembers the work by every id it carries, so a scan, a follow or a list finding it by any of them leaves
// it out.
func (e Exclusions) Exclude(ctx context.Context, at time.Time, mediaType, title string, ids []ExternalIDWrite) error {
	err := e.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, id := range ids {
			if err := queries.AddWorkExclusion(ctx, librarydb.AddWorkExclusionParams{
				MediaType: mediaType, Provider: id.Provider, ExternalID: id.ExternalID, Title: title, CreatedAt: at.Unix(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: exclude %q: %w", title, err)
	}
	return nil
}

// Excluded reports whether any of the ids belongs to an excluded work.
func (e Exclusions) Excluded(ctx context.Context, ids []ExternalIDWrite) (bool, error) {
	queries := librarydb.New(e.store.Read())
	for _, id := range ids {
		count, err := queries.CountWorkExclusion(ctx, librarydb.CountWorkExclusionParams{Provider: id.Provider, ExternalID: id.ExternalID})
		if err != nil {
			return false, fmt.Errorf("library: excluded work: %w", err)
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Unexclude forgets the exclusion carried by any of the ids; the work may be added again.
func (e Exclusions) Unexclude(ctx context.Context, ids []ExternalIDWrite) error {
	err := e.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, id := range ids {
			if err := queries.DeleteWorkExclusion(ctx, librarydb.DeleteWorkExclusionParams{Provider: id.Provider, ExternalID: id.ExternalID}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: unexclude: %w", err)
	}
	return nil
}

// List answers the excluded works of one media type, by title; one work appears once per id it was excluded by.
func (e Exclusions) List(ctx context.Context, mediaType string) ([]WorkExclusion, error) {
	rows, err := librarydb.New(e.store.Read()).ListWorkExclusions(ctx, mediaType)
	if err != nil {
		return nil, fmt.Errorf("library: list exclusions: %w", err)
	}
	out := make([]WorkExclusion, len(rows))
	for i, row := range rows {
		out[i] = WorkExclusion{MediaType: row.MediaType, Provider: row.Provider, ExternalID: row.ExternalID, Title: row.Title, CreatedAt: row.CreatedAt}
	}
	return out, nil
}
