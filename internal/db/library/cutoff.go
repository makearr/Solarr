package library

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// CutoffItem is one monitored album with all its tracks, as M1d's cutoff check reads it.
type CutoffItem struct {
	ID, WorkID, CategoryID int64
	State                  string
	PresentCount           int64
}

// CutoffPage pages through monitored COMPLETE and UPGRADABLE albums by id.
func (d Desired) CutoffPage(ctx context.Context, afterID, limit int64) ([]CutoffItem, error) {
	rows, err := librarydb.New(d.store.Read()).ListAlbumItemsForCutoff(ctx, librarydb.ListAlbumItemsForCutoffParams{
		AfterID: afterID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: cutoff page: %w", err)
	}
	items := make([]CutoffItem, len(rows))
	for index, row := range rows {
		items[index] = CutoffItem{
			ID: row.ID, WorkID: row.WorkID, CategoryID: row.CategoryID, State: row.State, PresentCount: row.PresentCount,
		}
	}
	return items, nil
}

// MeasuredFile is what probing measured of one of an album's files; Measured is false until it has.
type MeasuredFile struct {
	Measured    bool
	AudioCodec  string
	Extension   string
	BitrateKbps int64
	Lossless    bool
}

// MeasuredFiles answers what the probe measured of each file of a work's album.
func (d Desired) MeasuredFiles(ctx context.Context, workID int64) ([]MeasuredFile, error) {
	rows, err := librarydb.New(d.store.Read()).ListPartFilesForWork(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: measured files of work %d: %w", workID, err)
	}
	files := make([]MeasuredFile, len(rows))
	for index, row := range rows {
		files[index] = MeasuredFile{
			Measured: row.AudioCodec.Valid, AudioCodec: row.AudioCodec.String,
			Extension:   strings.ToLower(strings.TrimPrefix(path.Ext(row.RelPath), ".")),
			BitrateKbps: bitrateKbps(row.OverallBitrate, row.AudioBitrate),
			Lossless:    row.Lossless.Valid && row.Lossless.Int64 == 1,
		}
	}
	return files, nil
}

// SetAlbumState records an album item's COMPLETE or UPGRADABLE, keeping its present count.
func (d Desired) SetAlbumState(ctx context.Context, itemID int64, state string, presentCount int64) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
			State: state, PresentCount: presentCount, ID: itemID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set item %d state: %w", itemID, err)
	}
	return nil
}
