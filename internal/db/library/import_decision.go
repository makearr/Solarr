package library

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Import decision outcomes (import_decision.outcome) a replacement records.
const (
	ImportDecisionImport      = "IMPORT"
	ImportDecisionRejectWorse = "REJECT_WORSE"
)

// ImportDecisions keeps why a file replaced another, or was turned away, at settling time.
type ImportDecisions struct{ store *store.Store }

// NewImportDecisions builds it over an open library store.
func NewImportDecisions(opened *store.Store) ImportDecisions { return ImportDecisions{store: opened} }

// ImportDecision is one import_decision row; the JSON columns are the comparison as the judge explained it.
type ImportDecision struct {
	ID              int64
	DesiredItemID   int64
	IncumbentObject int64
	ArrivedProbe    string
	Comparison      string
	Outcome         string
	Reason          string
	DecidedAt       int64
}

// Record stores one decision.
func (d ImportDecisions) Record(ctx context.Context, decision ImportDecision) error {
	err := d.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).InsertImportDecision(ctx, librarydb.InsertImportDecisionParams{
			DesiredItemID: decision.DesiredItemID, IncumbentObjectID: sql.NullInt64{Int64: decision.IncumbentObject, Valid: decision.IncumbentObject != 0},
			ArrivedProbeJson: decision.ArrivedProbe, ComparisonJson: decision.Comparison, Outcome: decision.Outcome,
			ReasonJson: decision.Reason, DecidedAt: decision.DecidedAt,
		})
	})
	if err != nil {
		return fmt.Errorf("library: record import decision for item %d: %w", decision.DesiredItemID, err)
	}
	return nil
}

// ForWork answers a work's decisions, newest first.
func (d ImportDecisions) ForWork(ctx context.Context, workID int64, limit int) ([]ImportDecision, error) {
	rows, err := librarydb.New(d.store.Read()).ListImportDecisionsForWork(ctx, librarydb.ListImportDecisionsForWorkParams{
		WorkID: workID, PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("library: import decisions of work %d: %w", workID, err)
	}
	decisions := make([]ImportDecision, len(rows))
	for i, row := range rows {
		decisions[i] = ImportDecision{
			ID: row.ID, DesiredItemID: row.DesiredItemID, IncumbentObject: row.IncumbentObjectID.Int64,
			ArrivedProbe: row.ArrivedProbeJson, Comparison: row.ComparisonJson, Outcome: row.Outcome,
			Reason: row.ReasonJson, DecidedAt: row.DecidedAt,
		}
	}
	return decisions, nil
}

// RecordImportDecision stores why a file replaced another or was turned away, dated now when the decision is not.
func (d Desired) RecordImportDecision(ctx context.Context, decision ImportDecision) error {
	if decision.DecidedAt == 0 {
		decision.DecidedAt = d.store.Now().Unix()
	}
	return ImportDecisions(d).Record(ctx, decision)
}
