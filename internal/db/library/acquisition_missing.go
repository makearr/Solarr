package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// MissingSince answers when the download client first failed to list a download, found false while it lists it.
func (a Acquisitions) MissingSince(ctx context.Context, acquisitionID int64) (since int64, found bool, err error) {
	since, err = librarydb.New(a.store.Read()).GetAcquisitionMissingSince(ctx, acquisitionID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: missing mark of acquisition %d: %w", acquisitionID, err)
	}
	return since, true, nil
}

// MarkMissing records the first poll that did not see a download, keeping the earlier mark when there is one, and answers it.
func (a Acquisitions) MarkMissing(ctx context.Context, acquisitionID, at int64) (int64, error) {
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).MarkAcquisitionMissing(ctx, librarydb.MarkAcquisitionMissingParams{AcquisitionID: acquisitionID, Since: at})
	})
	if err != nil {
		return 0, fmt.Errorf("library: mark acquisition %d missing: %w", acquisitionID, err)
	}
	since, _, err := a.MissingSince(ctx, acquisitionID)
	return since, err
}

// ClearMissing forgets the mark once the client lists the download again.
func (a Acquisitions) ClearMissing(ctx context.Context, acquisitionID int64) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeleteAcquisitionMissing(ctx, acquisitionID)
	})
}
