package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Hunt is the planner's side of desired_item and hunt_queue (ADR-6, section 9).
type Hunt struct{ store *store.Store }

// NewHunt builds it over an open library store.
func NewHunt(opened *store.Store) Hunt { return Hunt{store: opened} }

// PlanInput is one desired item as the planner reads it.
type PlanInput struct {
	ID, CategoryID int64
	MediaRef       string
	Monitored      bool
	// SatisfactionKind is SET for a whole album or season, SINGLE for one track or episode.
	SatisfactionKind string
	State            string
	Generation       int64
	// ReleaseDate is the work's earliest street date, at whatever precision is stored.
	ReleaseDate                 string
	LastSearchedAt, SearchCount int64
	QueuedSince                 int64
}

// PlanInputs pages through the dirty set, or with dirtyOnly false through every item, by id.
func (h Hunt) PlanInputs(ctx context.Context, dirtyOnly bool, afterID, pageSize int64) ([]PlanInput, error) {
	rows, err := librarydb.New(h.store.Read()).ListPlanInputs(ctx, librarydb.ListPlanInputsParams{
		Kind: WorkDateStreet, DirtyOnly: boolToInt(dirtyOnly), AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list plan inputs: %w", err)
	}
	inputs := make([]PlanInput, len(rows))
	for index, row := range rows {
		inputs[index] = PlanInput{
			ID: row.ID, CategoryID: row.CategoryID, MediaRef: row.MediaRef, Monitored: row.Monitored == 1, SatisfactionKind: row.SatisfactionKind,
			State: row.State, Generation: row.Generation, ReleaseDate: row.ReleaseDate,
			LastSearchedAt: row.LastSearchedAt, SearchCount: row.SearchCount, QueuedSince: row.QueuedSince,
		}
	}
	return inputs, nil
}

// MaxItemID sizes the paced sweep.
func (h Hunt) MaxItemID(ctx context.Context) (int64, error) {
	maxID, err := librarydb.New(h.store.Read()).MaxDesiredItemID(ctx)
	if err != nil {
		return 0, fmt.Errorf("library: max desired item id: %w", err)
	}
	return maxID, nil
}

// PlannedHunt is one item's planned hunt_queue row, with the facts it was planned from.
type PlannedHunt struct {
	Input         PlanInput
	Class         string
	Priority      int64
	ReleaseDate   *int64
	EligibleAfter int64
	QueuedSince   int64
}

// ApplyPlan writes planned rows in bounded batches; an item changed since it was read keeps its
// dirty flag and hunt row for the next pass. Answers how many were applied.
func (h Hunt) ApplyPlan(ctx context.Context, planned []PlannedHunt, at int64) (int, error) {
	applied := 0
	err := store.WriteBatched(ctx, h.store, store.PrioBulk, insertChunk, planned,
		func(ctx context.Context, tx *sql.Tx, chunk []PlannedHunt) error {
			queries := librarydb.New(tx)
			for _, entry := range chunk {
				changed, err := queries.MarkDesiredItemPlannedIfUnchanged(ctx, librarydb.MarkDesiredItemPlannedIfUnchangedParams{
					LastPlannedAt: sql.NullInt64{Int64: at, Valid: true}, ID: entry.Input.ID,
					Generation: entry.Input.Generation, State: entry.Input.State, Monitored: boolToInt(entry.Input.Monitored),
				})
				if err != nil {
					return err
				}
				if changed == 0 {
					continue
				}
				if err := queries.UpsertPlannedHunt(ctx, librarydb.UpsertPlannedHuntParams{
					DesiredItemID: entry.Input.ID, Class: entry.Class, Priority: entry.Priority,
					ReleaseDate: nullInt64(entry.ReleaseDate), EligibleAfter: entry.EligibleAfter,
					QueuedSince: entry.QueuedSince,
				}); err != nil {
					return err
				}
				applied++
			}
			return nil
		})
	if err != nil {
		return applied, fmt.Errorf("library: apply plan: %w", err)
	}
	return applied, nil
}

// NextToHunt answers up to limit missing items eligible at now, most urgent and least recently
// searched first (section 9.3's default mode).
func (h Hunt) NextToHunt(ctx context.Context, now, limit int64) ([]int64, error) {
	rows, err := librarydb.New(h.store.Read()).NextHuntByOldestSearched(ctx, librarydb.NextHuntByOldestSearchedParams{
		Class: "missing", Now: now, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: next to hunt: %w", err)
	}
	ids := make([]int64, len(rows))
	for index, row := range rows {
		ids[index] = row.DesiredItemID
	}
	return ids, nil
}

// Postpone keeps the hunt away from itemID until at.
func (h Hunt) Postpone(ctx context.Context, itemID, at int64) error {
	err := h.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetHuntEligibleAfter(ctx, librarydb.SetHuntEligibleAfterParams{
			EligibleAfter: at, DesiredItemID: itemID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: postpone hunt for item %d: %w", itemID, err)
	}
	return nil
}
