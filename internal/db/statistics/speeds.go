package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// DownloadSpeeds records how long each download client took per download (F34); shown, never used to choose a client.
type DownloadSpeeds struct{ store *store.Store }

// NewDownloadSpeeds builds it over an open statistics store.
func NewDownloadSpeeds(opened *store.Store) DownloadSpeeds { return DownloadSpeeds{store: opened} }

// ClientSpeed is one client's downloads since a time.
type ClientSpeed struct {
	DownloaderID int64
	Downloads    int64
	SizeBytes    int64
	Seconds      int64
}

// BytesPerSecond is the average over the downloads, 0 when none took any time.
func (s ClientSpeed) BytesPerSecond() int64 {
	if s.Seconds <= 0 {
		return 0
	}
	return s.SizeBytes / s.Seconds
}

// Record logs one finished download.
func (d DownloadSpeeds) Record(ctx context.Context, downloaderID, sizeBytes, seconds int64, at time.Time) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return statisticsdb.New(tx).InsertDownloadSpeed(ctx, statisticsdb.InsertDownloadSpeedParams{
			At: at.Unix(), DownloaderID: downloaderID, SizeBytes: sizeBytes, Seconds: seconds,
		})
	})
	if err != nil {
		return fmt.Errorf("statistics: record download speed: %w", err)
	}
	return nil
}

// ByClient sums the downloads logged since a time, per client.
func (d DownloadSpeeds) ByClient(ctx context.Context, since time.Time) ([]ClientSpeed, error) {
	rows, err := statisticsdb.New(d.store.Read()).AggregateDownloadSpeedByClient(ctx, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("statistics: download speeds: %w", err)
	}
	speeds := make([]ClientSpeed, len(rows))
	for i, row := range rows {
		speeds[i] = ClientSpeed{DownloaderID: row.DownloaderID, Downloads: row.Downloads, SizeBytes: row.SizeBytes, Seconds: row.Seconds}
	}
	return speeds, nil
}

// Retire deletes rows logged before a time, a page per transaction, at most maxPages.
func (d DownloadSpeeds) Retire(ctx context.Context, before time.Time, maxPages int) (int64, error) {
	var total int64
	for range maxPages {
		var removed int64
		err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			removed, err = statisticsdb.New(tx).DeleteDownloadSpeedBefore(ctx, statisticsdb.DeleteDownloadSpeedBeforeParams{
				Before: before.Unix(), PageSize: retentionPage,
			})
			return err
		})
		if err != nil {
			return total, fmt.Errorf("statistics: retire download speeds: %w", err)
		}
		total += removed
		if removed < retentionPage {
			break
		}
	}
	return total, nil
}
