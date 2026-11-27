package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// SystemMetrics satisfies apiserver.SystemMetrics over the series kept in statistics.db.
type SystemMetrics struct {
	Metrics *statistics.Metrics
	Clock   clock.Clock
}

// Scopes lists every measured series.
func (a SystemMetrics) Scopes(ctx context.Context) ([]apiserver.MetricScopeRow, error) {
	scopes, err := a.Metrics.Scopes(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]apiserver.MetricScopeRow, len(scopes))
	for i, scope := range scopes {
		rows[i] = apiserver.MetricScopeRow{Series: scope.Series, Scope: scope.Scope}
	}
	return rows, nil
}

// Series lists one series from a time on.
func (a SystemMetrics) Series(ctx context.Context, series, scope string, since time.Time) ([]apiserver.MetricPointRow, int, error) {
	points, resolution, err := a.Metrics.Range(ctx, series, scope, since, a.Clock.Now())
	if err != nil {
		return nil, 0, err
	}
	rows := make([]apiserver.MetricPointRow, len(points))
	for i, point := range points {
		rows[i] = apiserver.MetricPointRow{AtUnix: point.At.Unix(), Value: point.Value}
	}
	return rows, resolution, nil
}
