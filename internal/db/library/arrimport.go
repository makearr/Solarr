package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// ArrImport states (todo 4.13, ADR-52).
const (
	ArrImportRunning  = "RUNNING"
	ArrImportDone     = "DONE"
	ArrImportFailed   = "FAILED"
	ArrImportReverted = "REVERTED"
)

// ArrImportItem kinds. "parked" is every app but Lidarr -- connected and
// listed, its own payload stored verbatim, nothing mapped yet (ADR-52).
const (
	ArrImportItemArtist = "artist"
	ArrImportItemAlbum  = "album"
	ArrImportItemParked = "parked"
)

// ArrImportCounts is a run's own outcome tally, recorded once at Finish.
type ArrImportCounts struct {
	ArtistsCreated, ArtistsMatched int64
	AlbumsCreated, AlbumsMatched   int64
	ItemsParked                    int64
}

// ArrImportRun is one *Arr import's own progress and outcome.
type ArrImportRun struct {
	ID         int64
	InstanceID int64
	App        string
	State      string
	StartedAt  int64
	FinishedAt *int64
	Error      string
	ArrImportCounts
}

// NewArrImportItem is one entity a run touched, ready to record -- the
// *_created flags are ArrImportRevert's own contract: only a row this run
// itself created may be undone, the same "created vs only matched" shape
// library.ImportRevert already uses for import_unit.work_created.
type NewArrImportItem struct {
	Kind               string
	SourceID           string
	Title              string
	PersonID           *int64
	PersonCreated      bool
	SubscriptionID     *int64
	WorkID             *int64
	WorkCreated        bool
	DesiredItemID      *int64
	DesiredItemCreated bool
	// PayloadJSON is a parked item's own source payload, stored verbatim
	// (ADR-52); empty for kind artist/album.
	PayloadJSON string
}

// ArrImportItem is one recorded item, read back.
type ArrImportItem struct {
	ID       int64
	ImportID int64
	NewArrImportItem
	CreatedAt int64
}

// ArrImports reads and writes arr_import/arr_import_item -- the revert-
// tracking shape import_session/import_unit already have for a normal
// import, one level up (one row per run, not per WALK cluster).
type ArrImports struct{ store *store.Store }

// NewArrImports builds one over an open library store.
func NewArrImports(opened *store.Store) ArrImports { return ArrImports{store: opened} }

// Start records a new run, state RUNNING.
func (a ArrImports) Start(ctx context.Context, instanceID int64, app string, startedAt int64) (int64, error) {
	var id int64
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).CreateArrImport(ctx, librarydb.CreateArrImportParams{
			InstanceID: instanceID, App: app, State: ArrImportRunning, StartedAt: startedAt,
		})
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: start arr import: %w", err)
	}
	return id, nil
}

// AddItem records one entity a run touched. One write per item rather than
// a batch: a Lidarr library is small enough (thousands of albums at most,
// never the 500k-file scale WALK/GROUP is built for) that per-item recording
// keeps partial progress visible and survivable, the same tolerance
// commitCluster's own per-unit loop already has for a bad file mid-batch.
func (a ArrImports) AddItem(ctx context.Context, importID int64, item NewArrImportItem, at int64) (int64, error) {
	var id int64
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).AddArrImportItem(ctx, librarydb.AddArrImportItemParams{
			ImportID: importID, Kind: item.Kind, SourceID: item.SourceID, Title: item.Title,
			PersonID: nullInt64(item.PersonID), PersonCreated: boolToInt(item.PersonCreated),
			SubscriptionID: nullInt64(item.SubscriptionID),
			WorkID:         nullInt64(item.WorkID), WorkCreated: boolToInt(item.WorkCreated),
			DesiredItemID: nullInt64(item.DesiredItemID), DesiredItemCreated: boolToInt(item.DesiredItemCreated),
			PayloadJson: nullString(item.PayloadJSON), CreatedAt: at,
		})
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: add arr import item to run %d: %w", importID, err)
	}
	return id, nil
}

// Finish records a run's terminal outcome.
func (a ArrImports) Finish(
	ctx context.Context, importID int64, state string, finishedAt int64, errMsg string, counts ArrImportCounts,
) error {
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).FinishArrImport(ctx, librarydb.FinishArrImportParams{
			State: state, FinishedAt: sql.NullInt64{Int64: finishedAt, Valid: true}, Error: nullString(errMsg),
			ArtistsCreated: counts.ArtistsCreated, ArtistsMatched: counts.ArtistsMatched,
			AlbumsCreated: counts.AlbumsCreated, AlbumsMatched: counts.AlbumsMatched,
			ItemsParked: counts.ItemsParked, ID: importID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: finish arr import %d: %w", importID, err)
	}
	return nil
}

// setState is ArrImportRevert's own use: flip a finished run to REVERTED
// once its items are undone. Unexported -- every other transition (RUNNING
// to DONE/FAILED with its own counts) goes through Finish instead.
func (a ArrImports) setState(ctx context.Context, importID int64, state string) error {
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetArrImportState(ctx, librarydb.SetArrImportStateParams{
			State: state, ID: importID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set arr import %d state: %w", importID, err)
	}
	return nil
}

// Get answers one run.
func (a ArrImports) Get(ctx context.Context, id int64) (ArrImportRun, error) {
	row, err := librarydb.New(a.store.Read()).GetArrImport(ctx, id)
	if err != nil {
		return ArrImportRun{}, fmt.Errorf("library: get arr import %d: %w", id, err)
	}
	return arrImportRunFrom(row), nil
}

// ListForInstance answers every run for one instance, newest first.
func (a ArrImports) ListForInstance(ctx context.Context, instanceID int64) ([]ArrImportRun, error) {
	rows, err := librarydb.New(a.store.Read()).ListArrImportsForInstance(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("library: list arr imports for instance %d: %w", instanceID, err)
	}
	out := make([]ArrImportRun, len(rows))
	for index, row := range rows {
		out[index] = arrImportRunFrom(row)
	}
	return out, nil
}

// ListItems answers every item a run touched, in the order they were added.
func (a ArrImports) ListItems(ctx context.Context, importID int64) ([]ArrImportItem, error) {
	rows, err := librarydb.New(a.store.Read()).ListArrImportItems(ctx, importID)
	if err != nil {
		return nil, fmt.Errorf("library: list arr import items for run %d: %w", importID, err)
	}
	out := make([]ArrImportItem, len(rows))
	for index, row := range rows {
		out[index] = arrImportItemFrom(row)
	}
	return out, nil
}

func arrImportRunFrom(row librarydb.ArrImport) ArrImportRun {
	return ArrImportRun{
		ID: row.ID, InstanceID: row.InstanceID, App: row.App, State: row.State,
		StartedAt: row.StartedAt, FinishedAt: int64Ptr(row.FinishedAt), Error: row.Error.String,
		ArrImportCounts: ArrImportCounts{
			ArtistsCreated: row.ArtistsCreated, ArtistsMatched: row.ArtistsMatched,
			AlbumsCreated: row.AlbumsCreated, AlbumsMatched: row.AlbumsMatched,
			ItemsParked: row.ItemsParked,
		},
	}
}

func arrImportItemFrom(row librarydb.ArrImportItem) ArrImportItem {
	return ArrImportItem{
		ID: row.ID, ImportID: row.ImportID,
		NewArrImportItem: NewArrImportItem{
			Kind: row.Kind, SourceID: row.SourceID, Title: row.Title,
			PersonID: int64Ptr(row.PersonID), PersonCreated: row.PersonCreated != 0,
			SubscriptionID: int64Ptr(row.SubscriptionID),
			WorkID:         int64Ptr(row.WorkID), WorkCreated: row.WorkCreated != 0,
			DesiredItemID: int64Ptr(row.DesiredItemID), DesiredItemCreated: row.DesiredItemCreated != 0,
			PayloadJSON: row.PayloadJson.String,
		},
		CreatedAt: row.CreatedAt,
	}
}
