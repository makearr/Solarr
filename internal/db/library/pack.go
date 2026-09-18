package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/importing/pack"
)

// Pack kinds and detection sources (pack.kind/detected_from, migration
// 0026) -- internal/importing/pack.Detect's own values (pure, ADR-23),
// re-exported as plain strings for this file's own callers, the same shape
// library.UnitSingle etc. already give inspect.Shape's values.
const (
	PackDiscography = pack.Discography
	PackBoxSet      = pack.BoxSet
	PackAnthology   = pack.Anthology
	PackCompilation = pack.Compilation

	PackDetectedFromStructure         = pack.DetectedFromStructure
	PackDetectedFromStructureAndTitle = pack.DetectedFromStructureAndTitle
)

// Pack item states (pack_item.state).
const (
	PackItemResolved = "RESOLVED"
	PackItemQueued   = "QUEUED"
	PackItemFailed   = "FAILED"
	PackItemSkipped  = "SKIPPED"
)

// Packs is ADR-50's own persistence: one row per detected pack, one row per
// member. internal/importing/pack.Detect is pure and in-memory (ADR-23);
// this is where a caller persists what it found.
type Packs struct{ store *store.Store }

// NewPacks builds it over an open library store.
func NewPacks(opened *store.Store) Packs { return Packs{store: opened} }

// Pack is one pack row.
type Pack struct {
	ID            int64
	Kind          string
	DetectedFrom  string
	Confidence    float64
	Title         string
	AcquisitionID *int64
	SessionID     *int64
	MemberCount   int64
	ResolvedCount int64
	CreatedAt     int64
}

// NewPackItem is what Create needs for one member, before it has a pack id
// of its own to reference.
type NewPackItem struct {
	WorkID       *int64
	EditionID    *int64
	ImportUnitID int64
	State        string
}

// PackItem is one pack_item row.
type PackItem struct {
	PackID       int64
	Ord          int64
	WorkID       *int64
	EditionID    *int64
	ImportUnitID *int64
	State        string
}

// Create records one detected pack and every one of its members in a single
// transaction, and stamps pack_id onto each member's own import_unit row --
// the same "one transaction, one recompute" shape AddUnits already uses.
func (p Packs) Create(
	ctx context.Context, kind, detectedFrom string, confidence float64, title string,
	sessionID int64, createdAt int64, items []NewPackItem,
) (int64, error) {
	var id int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		row, err := queries.CreatePack(ctx, librarydb.CreatePackParams{
			Kind: kind, DetectedFrom: detectedFrom, Confidence: confidence, Title: title,
			SessionID: sql.NullInt64{Int64: sessionID, Valid: true}, MemberCount: int64(len(items)),
			CreatedAt: createdAt,
		})
		if err != nil {
			return fmt.Errorf("create pack: %w", err)
		}
		id = row.ID
		for i, item := range items {
			if err := queries.InsertPackItem(ctx, librarydb.InsertPackItemParams{
				PackID: id, Ord: int64(i), WorkID: nullInt64(item.WorkID),
				EditionID:    nullInt64(item.EditionID),
				ImportUnitID: sql.NullInt64{Int64: item.ImportUnitID, Valid: true},
				State:        item.State,
			}); err != nil {
				return fmt.Errorf("insert pack item for unit %d: %w", item.ImportUnitID, err)
			}
			if err := queries.SetImportUnitPack(ctx, librarydb.SetImportUnitPackParams{
				PackID: sql.NullInt64{Int64: id, Valid: true}, ID: item.ImportUnitID,
			}); err != nil {
				return fmt.Errorf("set unit %d pack: %w", item.ImportUnitID, err)
			}
		}
		return queries.RecomputePackCounts(ctx, id)
	})
	if err != nil {
		return 0, fmt.Errorf("library: create pack: %w", err)
	}
	return id, nil
}

// ResolveItem moves one pack member from QUEUED to RESOLVED once a later
// action (ManualCommitter's own "assign a category later" or "commit what
// was held") places the unit it belongs to -- a no-op, safely, for a unit
// that was never part of any pack (no row matches importUnitID).
func (p Packs) ResolveItem(ctx context.Context, importUnitID, workID int64, editionID *int64) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.ResolvePackItem(ctx, librarydb.ResolvePackItemParams{
			ImportUnitID: sql.NullInt64{Int64: importUnitID, Valid: true},
			WorkID:       sql.NullInt64{Int64: workID, Valid: true}, EditionID: nullInt64(editionID),
		}); err != nil {
			return fmt.Errorf("resolve pack item for unit %d: %w", importUnitID, err)
		}
		row, err := queries.GetImportUnit(ctx, importUnitID)
		if err != nil {
			return fmt.Errorf("get unit %d: %w", importUnitID, err)
		}
		if !row.PackID.Valid {
			return nil
		}
		return queries.RecomputePackCounts(ctx, row.PackID.Int64)
	})
}

// Get answers one pack by id. A missing id surfaces sql.ErrNoRows, wrapped.
func (p Packs) Get(ctx context.Context, id int64) (Pack, error) {
	row, err := librarydb.New(p.store.Read()).GetPack(ctx, id)
	if err != nil {
		return Pack{}, fmt.Errorf("library: get pack %d: %w", id, err)
	}
	return packFromRow(row), nil
}

// ListPage returns one cursor-paginated page of packs, newest-created last
// (plain ascending id, the same cursor /blocked and import_unit's own page
// already use).
func (p Packs) ListPage(ctx context.Context, afterID int64, pageSize int64) ([]Pack, error) {
	rows, err := librarydb.New(p.store.Read()).ListPacksPage(ctx, librarydb.ListPacksPageParams{
		AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list packs: %w", err)
	}
	packs := make([]Pack, len(rows))
	for i, row := range rows {
		packs[i] = packFromRow(row)
	}
	return packs, nil
}

// Items answers one pack's own members, in the order they were recorded.
func (p Packs) Items(ctx context.Context, packID int64) ([]PackItem, error) {
	rows, err := librarydb.New(p.store.Read()).ListPackItems(ctx, packID)
	if err != nil {
		return nil, fmt.Errorf("library: list items for pack %d: %w", packID, err)
	}
	items := make([]PackItem, len(rows))
	for i, row := range rows {
		items[i] = PackItem{
			PackID: row.PackID, Ord: row.Ord, WorkID: int64Ptr(row.WorkID),
			EditionID: int64Ptr(row.EditionID), ImportUnitID: int64Ptr(row.ImportUnitID), State: row.State,
		}
	}
	return items, nil
}

func packFromRow(row librarydb.Pack) Pack {
	return Pack{
		ID: row.ID, Kind: row.Kind, DetectedFrom: row.DetectedFrom, Confidence: row.Confidence,
		Title: row.Title, AcquisitionID: int64Ptr(row.AcquisitionID), SessionID: int64Ptr(row.SessionID),
		MemberCount: row.MemberCount, ResolvedCount: row.ResolvedCount, CreatedAt: row.CreatedAt,
	}
}
