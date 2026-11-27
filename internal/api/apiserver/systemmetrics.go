package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// MetricPointRow is one value of a measured series.
type MetricPointRow struct {
	AtUnix int64
	Value  float64
}

// MetricScopeRow names a measured series and what it is measured of.
type MetricScopeRow struct{ Series, Scope string }

// SystemMetrics is what Solarr measured of itself over time; cmd/solarr/adapters answers it from statistics.db.
type SystemMetrics interface {
	Scopes(ctx context.Context) ([]MetricScopeRow, error)
	// Series lists a series from a time on and the seconds each point covers.
	Series(ctx context.Context, series, scope string, since time.Time) ([]MetricPointRow, int, error)
}

// metricRanges are the spans a chart can ask for.
var metricRanges = map[string]time.Duration{
	"hour": time.Hour, "6hours": 6 * time.Hour, "day": 24 * time.Hour, "week": 7 * 24 * time.Hour, "month": 30 * 24 * time.Hour,
}

// GetSystemMetricScopes lists what has been measured.
func (s *Server) GetSystemMetricScopes(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	body := gen.SystemMetricScopes{Scopes: []gen.SystemMetricScope{}}
	if s.systemMetrics != nil {
		rows, err := s.systemMetrics.Scopes(r.Context())
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		for _, row := range rows {
			body.Scopes = append(body.Scopes, gen.SystemMetricScope{Series: row.Series, Scope: row.Scope})
		}
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

// GetSystemMetricSeries answers one measured series over a span.
func (s *Server) GetSystemMetricSeries(w http.ResponseWriter, r *http.Request, params gen.GetSystemMetricSeriesParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	span := metricRanges["day"]
	if params.Range != nil {
		if requested, known := metricRanges[string(*params.Range)]; known {
			span = requested
		}
	}
	scope, series := "", params.Series
	if params.Scope != nil {
		scope = *params.Scope
	}
	body := gen.SystemMetricSeries{Series: series, Scope: scope, ResolutionSeconds: 60, Points: []gen.SystemMetricPoint{}}
	if s.systemMetrics != nil {
		rows, resolution, err := s.systemMetrics.Series(r.Context(), series, scope, s.auth.Clock.Now().Add(-span))
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		body.ResolutionSeconds = resolution
		for _, row := range rows {
			body.Points = append(body.Points, gen.SystemMetricPoint{At: row.AtUnix, Value: row.Value})
		}
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}
