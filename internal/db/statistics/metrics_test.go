package statistics_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/statistics/statisticstest"
)

func TestMetrics_RollUpKeepsAveragesAndPrunesMinutes(t *testing.T) {
	metrics := statistics.NewMetrics(statisticstest.Open(t))
	ctx := t.Context()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i, value := range []float64{10, 20, 30} {
		err := metrics.Record(ctx, base.Add(time.Duration(i)*time.Minute), []statistics.MetricSample{{Series: "process.cpu", Value: value}})
		if err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(3 * time.Hour)
	if err := metrics.Maintain(ctx, now); err != nil {
		t.Fatal(err)
	}
	minutes, resolution, err := metrics.Range(ctx, "process.cpu", "", base, now)
	if err != nil || resolution != 60 || len(minutes) != 3 {
		t.Fatalf("minutes = %v resolution %d err %v", minutes, resolution, err)
	}
	later := base.Add(72 * time.Hour)
	if err := metrics.Maintain(ctx, later); err != nil {
		t.Fatal(err)
	}
	hours, resolution, err := metrics.Range(ctx, "process.cpu", "", base, later)
	if err != nil || resolution != 3600 {
		t.Fatalf("resolution %d err %v", resolution, err)
	}
	if len(hours) != 1 || hours[0].Value != 20 || !hours[0].At.Equal(base) {
		t.Fatalf("hours = %+v", hours)
	}
	scopes, err := metrics.Scopes(ctx)
	if err != nil || len(scopes) != 1 || scopes[0].Series != "process.cpu" {
		t.Fatalf("scopes = %v %v", scopes, err)
	}
}
