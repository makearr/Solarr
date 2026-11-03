package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/statistics"
)

// A client's speed is its bytes over its seconds since a time, not an average of averages; retention removes old rows.
func TestDownloadSpeeds_SumPerClientAndRetire(t *testing.T) {
	speeds := statistics.NewDownloadSpeeds(openStatistics(t))
	ctx := t.Context()
	for _, row := range []struct{ client, size, seconds, at int64 }{
		{1, 1_000, 10, 100}, {1, 3_000, 10, 2_000}, {1, 5_000, 40, 3_000}, {2, 900, 3, 3_000},
	} {
		if err := speeds.Record(ctx, row.client, row.size, row.seconds, time.Unix(row.at, 0)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := speeds.ByClient(ctx, time.Unix(1_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	want := []statistics.ClientSpeed{{DownloaderID: 1, Downloads: 2, SizeBytes: 8_000, Seconds: 50}, {DownloaderID: 2, Downloads: 1, SizeBytes: 900, Seconds: 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("speeds = %+v, want %+v", got, want)
	}
	if got[0].BytesPerSecond() != 160 || got[1].BytesPerSecond() != 300 {
		t.Errorf("bytes per second = %d, %d; want 160, 300", got[0].BytesPerSecond(), got[1].BytesPerSecond())
	}
	removed, err := speeds.Retire(ctx, time.Unix(2_500, 0), 10)
	if err != nil || removed != 2 {
		t.Errorf("Retire = %d, %v; want 2, nil", removed, err)
	}
}
