package adapters

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
)

// Packs implements apiserver.PackLister over library.Packs.
type Packs struct{ Packs library.Packs }

func (a Packs) ListPacks(ctx context.Context, afterID int64, limit int) ([]apiserver.PackRow, error) {
	rows, err := a.Packs.ListPage(ctx, afterID, int64(limit))
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.PackRow, len(rows))
	for i, row := range rows {
		views[i] = packRow(row)
	}
	return views, nil
}

func (a Packs) GetPack(ctx context.Context, id int64) (apiserver.PackRow, []apiserver.PackItemRow, error) {
	row, err := a.Packs.Get(ctx, id)
	if err != nil {
		return apiserver.PackRow{}, nil, err
	}
	items, err := a.Packs.Items(ctx, id)
	if err != nil {
		return apiserver.PackRow{}, nil, err
	}
	views := make([]apiserver.PackItemRow, len(items))
	for i, item := range items {
		views[i] = apiserver.PackItemRow{
			Ord: item.Ord, WorkID: item.WorkID, EditionID: item.EditionID,
			ImportUnitID: item.ImportUnitID, State: item.State,
		}
	}
	return packRow(row), views, nil
}

func packRow(row library.Pack) apiserver.PackRow {
	return apiserver.PackRow{
		ID: row.ID, Kind: row.Kind, DetectedFrom: row.DetectedFrom, Confidence: row.Confidence,
		Title: row.Title, AcquisitionID: row.AcquisitionID, SessionID: row.SessionID,
		MemberCount: row.MemberCount, ResolvedCount: row.ResolvedCount,
		CreatedAt: time.Unix(row.CreatedAt, 0),
	}
}
