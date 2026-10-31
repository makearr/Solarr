package store_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestPageCache_CountsHitsAndSizesAfterReads(t *testing.T) {
	if store.DriverName() != "modernc" {
		t.Skip("page cache counters are read through modernc only")
	}
	opened := openWritableStore(t, clock.NewFakeAtEpoch(), func(config *store.Config) { config.ReadPoolSize = 2; config.CacheSizeKB = 4096 })
	if err := opened.Write(t.Context(), store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO work (title) VALUES ('Zor A'), ('Zor B')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if got := countRows(t, opened); got != 2 {
			t.Fatalf("rows = %d", got)
		}
	}

	report := opened.PageCache(t.Context())
	if report.Name != "library" || report.FileBytes <= 0 || report.PerConnectionKB != 4096 {
		t.Errorf("report %+v; want the library with a file on disk and a 4096 KB cache", report)
	}
	if report.Connections != 3 || report.LimitBytes != 3*4096*1024 {
		t.Errorf("connections %d, limit %d; want the writer and both readers", report.Connections, report.LimitBytes)
	}
	if report.Hits <= 0 || report.UsedBytes <= 0 {
		t.Errorf("hits %d, used %d; want both counted after repeated reads", report.Hits, report.UsedBytes)
	}
}
