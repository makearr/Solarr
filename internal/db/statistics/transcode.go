package statistics

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/statisticsdb"
)

// TranscodeDelta is what one finished or reverted conversion adds; a restore adds the negatives.
type TranscodeDelta struct {
	CategoryID                                               int64
	Kind                                                     string
	Files, OriginalBytes, ResultBytes, EncodeSeconds, Failed int64
}

// TranscodeTotal is the sum of the deltas of one library and kind, or of everything when CategoryID is 0.
type TranscodeTotal struct {
	CategoryID                                               int64
	Kind                                                     string
	Files, OriginalBytes, ResultBytes, EncodeSeconds, Failed int64
}

// Transcodes keeps what conversion saved.
type Transcodes struct{ store *store.Store }

// NewTranscodes builds it over an open statistics store.
func NewTranscodes(opened *store.Store) Transcodes { return Transcodes{store: opened} }

// DayOf is the UTC midnight, in unix seconds, of the day an instant falls in.
func DayOf(at time.Time) int64 {
	year, month, day := at.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Unix()
}

// Record adds the delta to the day's row of its library and kind.
func (t Transcodes) Record(ctx context.Context, day int64, delta TranscodeDelta) error {
	err := t.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return statisticsdb.New(tx).AddTranscodeStat(ctx, statisticsdb.AddTranscodeStatParams{
			CategoryID: delta.CategoryID, Day: day, Kind: delta.Kind, Files: delta.Files, OriginalBytes: delta.OriginalBytes,
			ResultBytes: delta.ResultBytes, EncodeSeconds: delta.EncodeSeconds, Failed: delta.Failed,
		})
	})
	if err != nil {
		return fmt.Errorf("statistics: record transcode: %w", err)
	}
	return nil
}

// Totals answers the sums per library and kind, and over everything.
func (t Transcodes) Totals(ctx context.Context) (perLibrary []TranscodeTotal, all TranscodeTotal, err error) {
	rows, err := statisticsdb.New(t.store.Read()).ListTranscodeStatTotals(ctx)
	if err != nil {
		return nil, TranscodeTotal{}, fmt.Errorf("statistics: transcode totals: %w", err)
	}
	for _, row := range rows {
		total := TranscodeTotal{CategoryID: row.CategoryID, Kind: row.Kind, Files: row.Files, OriginalBytes: row.OriginalBytes, ResultBytes: row.ResultBytes, EncodeSeconds: row.EncodeSeconds, Failed: row.Failed}
		perLibrary = append(perLibrary, total)
		all.Files += total.Files
		all.OriginalBytes += total.OriginalBytes
		all.ResultBytes += total.ResultBytes
		all.EncodeSeconds += total.EncodeSeconds
		all.Failed += total.Failed
	}
	return perLibrary, all, nil
}
