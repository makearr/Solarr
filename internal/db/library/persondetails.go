package library

import (
	"context"
	"fmt"
	"time"
)

// DetailsAsked is whether the person's details were read, or tried, since the time.
func (p People) DetailsAsked(ctx context.Context, personID int64, since time.Time) (bool, error) {
	var asked int64
	err := p.store.Read().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM person_refresh WHERE person_id = ? AND refreshed_at >= ?`, personID, since.Unix()).Scan(&asked)
	if err != nil {
		return false, fmt.Errorf("library: details asked for person %d: %w", personID, err)
	}
	return asked > 0, nil
}
