package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// StallState is how far a download had got when its progress was last checked, and how many checks in a row found it
// had not moved.
type StallState struct {
	Strikes    int
	LeftBytes  int64
	ProgressAt int64
	CheckedAt  int64
}

// Stall answers a download's stall state; found is false before its first check.
func (a Acquisitions) Stall(ctx context.Context, acquisitionID int64) (StallState, bool, error) {
	row, err := librarydb.New(a.store.Read()).GetAcquisitionStall(ctx, acquisitionID)
	if errors.Is(err, sql.ErrNoRows) {
		return StallState{}, false, nil
	}
	if err != nil {
		return StallState{}, false, fmt.Errorf("library: stall of acquisition %d: %w", acquisitionID, err)
	}
	return StallState{Strikes: int(row.Strikes), LeftBytes: row.LeftBytes, ProgressAt: row.ProgressAt, CheckedAt: row.CheckedAt}, true, nil
}

// SetStall records a download's stall state.
func (a Acquisitions) SetStall(ctx context.Context, acquisitionID int64, state StallState) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetAcquisitionStall(ctx, librarydb.SetAcquisitionStallParams{
			AcquisitionID: acquisitionID, Strikes: int64(state.Strikes), LeftBytes: state.LeftBytes,
			ProgressAt: state.ProgressAt, CheckedAt: state.CheckedAt,
		})
	})
}

// ForgetStall drops a download's stall state, so its clock starts again the next time it downloads.
func (a Acquisitions) ForgetStall(ctx context.Context, acquisitionID int64) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteAcquisitionStall(ctx, acquisitionID)
	})
}
