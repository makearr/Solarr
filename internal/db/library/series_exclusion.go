package library

import (
	"context"
	"time"
)

// SetManualField is Works.SetManualField, for the TV service that holds a Series.
func (s Series) SetManualField(ctx context.Context, at time.Time, workID int64, field, value string) error {
	return Works(s).SetManualField(ctx, at, workID, field, value)
}
