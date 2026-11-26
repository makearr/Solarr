package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// probeRetryCap is the longest a file ffprobe cannot read waits before it is asked again.
const probeRetryCap = 30 * 24 * time.Hour

// IncludeProbeFailures is the "now" that lists files whose probe failed as well, as the health report does.
const IncludeProbeFailures int64 = math.MaxInt64

// ProbeRetryAfter is how long a file waits after its attempts-th failed probe: a day, doubling, up to probeRetryCap.
func ProbeRetryAfter(attempts int64) time.Duration {
	wait := 24 * time.Hour
	for range attempts - 1 {
		wait *= 2
		if wait >= probeRetryCap {
			return probeRetryCap
		}
	}
	return wait
}

// RecordProbeFailure remembers that ffprobe could not read a file of this size, so the probe sweep leaves it alone
// until the wait is over or the file changes size.
func (p Physical) RecordProbeFailure(ctx context.Context, mediaObjectID, sizeBytes int64, reason, raw string, at time.Time) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		attempts, err := queries.GetProbeFailureAttempts(ctx, mediaObjectID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := queries.UpsertProbeFailure(ctx, librarydb.UpsertProbeFailureParams{
			MediaObjectID: mediaObjectID, LastError: reason, Raw: raw, At: at.Unix(),
			RetryAt: at.Add(ProbeRetryAfter(attempts + 1)).Unix(), SizeBytes: sizeBytes,
		}); err != nil {
			return err
		}
		// An empty file is defective at once; one that cannot be read, once a second probe agrees.
		switch {
		case sizeBytes == 0:
			return queries.UpsertFileDefect(ctx, librarydb.UpsertFileDefectParams{MediaObjectID: mediaObjectID, Reason: DefectEmpty, Detail: "", SizeBytes: 0, DetectedAt: at.Unix()})
		case attempts+1 >= defectAfterAttempts:
			return queries.UpsertFileDefect(ctx, librarydb.UpsertFileDefectParams{MediaObjectID: mediaObjectID, Reason: DefectUnreadable, Detail: reason, SizeBytes: sizeBytes, DetectedAt: at.Unix()})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: record probe failure %d: %w", mediaObjectID, err)
	}
	return nil
}

// CountProbeFailures is how many files the probe sweep is leaving alone because ffprobe could not read them.
func (p Physical) CountProbeFailures(ctx context.Context) (int64, error) {
	total, err := librarydb.New(p.store.Read()).CountProbeFailures(ctx)
	if err != nil {
		return 0, fmt.Errorf("library: count probe failures: %w", err)
	}
	return total, nil
}

// ForgetProbeFailures makes every unreadable file due again ("probe again").
func (p Physical) ForgetProbeFailures(ctx context.Context) (int64, error) {
	var forgotten int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var deleteErr error
		forgotten, deleteErr = librarydb.New(tx).DeleteAllProbeFailures(ctx)
		return deleteErr
	})
	if err != nil {
		return 0, fmt.Errorf("library: forget probe failures: %w", err)
	}
	return forgotten, nil
}

// ProbeFailureRaw is what ffprobe printed the last time it could not read the file; empty when it printed nothing.
func (p Physical) ProbeFailureRaw(ctx context.Context, mediaObjectID int64) (string, error) {
	raw, err := librarydb.New(p.store.Read()).GetProbeFailureRaw(ctx, mediaObjectID)
	if err != nil {
		return "", fmt.Errorf("library: probe failure raw %d: %w", mediaObjectID, err)
	}
	return raw, nil
}
