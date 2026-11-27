package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// Retention of the measured series: minutes for two days, hours for ninety.
const (
	MinuteKeep = 48 * time.Hour
	HourKeep   = 90 * 24 * time.Hour
)

// MetricPoint is one value of one series in one bucket.
type MetricPoint struct {
	At    time.Time
	Value float64
}

// MetricSample is one value to record for the minute that just ended.
type MetricSample struct {
	Series, Scope string
	Value         float64
}

// MetricScope names one series and what it is measured of.
type MetricScope struct{ Series, Scope string }

// Metrics keeps Solarr's own measured figures over time.
type Metrics struct{ store *store.Store }

// NewMetrics builds it over an open statistics store.
func NewMetrics(opened *store.Store) *Metrics { return &Metrics{store: opened} }

// Record writes the samples of the minute starting at minute, in chunks.
func (m *Metrics) Record(ctx context.Context, minute time.Time, samples []MetricSample) error {
	at := minute.UTC().Truncate(time.Minute).Unix()
	for start := 0; start < len(samples); start += writeChunk {
		chunk := samples[start:min(start+writeChunk, len(samples))]
		err := m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := statisticsdb.New(tx)
			for _, sample := range chunk {
				if err := queries.UpsertMetricPoint(ctx, statisticsdb.UpsertMetricPointParams{
					Resolution: 60, At: at, Series: sample.Series, Scope: sample.Scope, Value: sample.Value,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("statistics: record metrics: %w", err)
		}
	}
	return nil
}

// Range lists a series since a time, minutes for a span the minute rows cover and hours beyond.
func (m *Metrics) Range(ctx context.Context, series, scope string, since time.Time, now time.Time) ([]MetricPoint, int, error) {
	resolution := int64(60)
	if now.Sub(since) > MinuteKeep {
		resolution = 3600
	}
	rows, err := statisticsdb.New(m.store.Read()).ListMetricPoints(ctx, statisticsdb.ListMetricPointsParams{
		Series: series, Scope: scope, Resolution: resolution, Since: since.Unix(),
	})
	if err != nil {
		return nil, 0, fmt.Errorf("statistics: list metrics: %w", err)
	}
	points := make([]MetricPoint, len(rows))
	for i, row := range rows {
		points[i] = MetricPoint{At: time.Unix(row.At, 0).UTC(), Value: row.Value}
	}
	return points, int(resolution), nil
}

// Scopes lists every series with a value, and what each is measured of.
func (m *Metrics) Scopes(ctx context.Context) ([]MetricScope, error) {
	rows, err := statisticsdb.New(m.store.Read()).ListMetricScopes(ctx)
	if err != nil {
		return nil, fmt.Errorf("statistics: list metric scopes: %w", err)
	}
	scopes := make([]MetricScope, len(rows))
	for i, row := range rows {
		scopes[i] = MetricScope{Series: row.Series, Scope: row.Scope}
	}
	return scopes, nil
}

// Maintain rolls the finished hours' minutes into hour rows, then drops what is past its retention.
func (m *Metrics) Maintain(ctx context.Context, now time.Time) error {
	hour := now.UTC().Truncate(time.Hour)
	return m.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := statisticsdb.New(tx)
		if err := queries.RollUpMetricMinutes(ctx, statisticsdb.RollUpMetricMinutesParams{Since: hour.Add(-MinuteKeep).Unix(), Before: hour.Unix()}); err != nil {
			return err
		}
		if _, err := queries.DeleteMetricPointsBefore(ctx, statisticsdb.DeleteMetricPointsBeforeParams{Resolution: 60, Before: now.Add(-MinuteKeep).Unix()}); err != nil {
			return err
		}
		_, err := queries.DeleteMetricPointsBefore(ctx, statisticsdb.DeleteMetricPointsBeforeParams{Resolution: 3600, Before: now.Add(-HourKeep).Unix()})
		return err
	})
}
