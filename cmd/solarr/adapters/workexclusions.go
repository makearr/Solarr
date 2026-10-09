package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// WorkExclusions satisfies apiserver.WorkExclusions over library.Exclusions: a work is excluded by every id it had, so
// the rows of one removal (one title, one moment) are one entry.
type WorkExclusions struct{ Exclusions library.Exclusions }

// List answers one entry per removed work of a media type.
func (a WorkExclusions) List(ctx context.Context, mediaType string) ([]apiserver.ExclusionRow, error) {
	rows, err := a.Exclusions.List(ctx, mediaType)
	if err != nil {
		return nil, err
	}
	type removal struct {
		title string
		at    int64
	}
	seen := map[removal]bool{}
	entries := make([]apiserver.ExclusionRow, 0, len(rows))
	for _, row := range rows {
		if key := (removal{row.Title, row.CreatedAt}); !seen[key] {
			seen[key] = true
			entries = append(entries, apiserver.ExclusionRow{Title: row.Title, Provider: row.Provider, ExternalID: row.ExternalID, CreatedAt: row.CreatedAt})
		}
	}
	return entries, nil
}

// Allow lifts the removal the id belongs to, by every id of that removal.
func (a WorkExclusions) Allow(ctx context.Context, mediaType, provider, externalID string) error {
	rows, err := a.Exclusions.List(ctx, mediaType)
	if err != nil {
		return err
	}
	var ids []library.ExternalIDWrite
	for _, row := range rows {
		if row.Provider == provider && row.ExternalID == externalID {
			for _, sibling := range rows {
				if sibling.Title == row.Title && sibling.CreatedAt == row.CreatedAt {
					ids = append(ids, library.ExternalIDWrite{Provider: sibling.Provider, ExternalID: sibling.ExternalID})
				}
			}
		}
	}
	return a.Exclusions.Unexclude(ctx, ids)
}
