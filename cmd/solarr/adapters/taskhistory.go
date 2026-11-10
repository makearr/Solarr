package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/statistics"
)

// TaskHistory satisfies apiserver.TaskHistory over the hours kept in statistics.db.
type TaskHistory struct{ Log *statistics.TaskHours }

// Hours lists what ran from a time on.
func (a TaskHistory) Hours(ctx context.Context, since time.Time) ([]apiserver.TaskHourRow, error) {
	hours, err := a.Log.Since(ctx, since)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.TaskHourRow, len(hours))
	for i, hour := range hours {
		rows[i] = apiserver.TaskHourRow{JobType: hour.JobType, HourStartUnix: hour.HourStart.Unix(), RunMs: hour.RunMs, Runs: hour.Runs}
	}
	return rows, nil
}
