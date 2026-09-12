package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// Acquisition is section 12's own provenance record: a candidate handed to
// a download client, denormalised (CandidateGUID/CandidateTitle/SearchTerms)
// because the candidate it came from can expire (section 10.1) while this
// row must outlive it.
type Acquisition struct {
	ID                int64
	DesiredItemID     *int64
	CandidateID       *int64
	IndexerID         *int64
	CandidateGUID     string
	CandidateTitle    string
	SearchTerms       string
	DownloaderID      int64
	DownloadClientRef string
	DownloadDir       string
	ClientCategory    string
	ClientFingerprint string
	Protocol          string
	State             string
	LastError         string
	CreatedAt         int64
	UpdatedAt         int64
}

// NewAcquisition is what Create needs to record one dispatch (ADR-57's own
// acquire.send). DDL-only fields (ddl_resolved_url and friends) have no
// counterpart here yet -- usenet is the only protocol this pass dispatches.
type NewAcquisition struct {
	DesiredItemID     *int64
	CandidateID       *int64
	IndexerID         *int64
	CandidateGUID     string
	CandidateTitle    string
	SearchTerms       string
	DownloaderID      int64
	DownloadClientRef string
	ClientCategory    string
	ClientFingerprint string
	Protocol          string
	State             string
	CreatedAt         int64
	UpdatedAt         int64
}

// ErrAcquisitionAlreadyInFlight reports a desired item that already has a
// non-terminal acquisition -- Invariant 15's own "at most one in-flight
// acquisition per desired item", checked here first so a caller gets a
// clean, mapped refusal rather than the acquisition_one_inflight unique
// index's own raw constraint error.
var ErrAcquisitionAlreadyInFlight = errors.New("library: desired item already has an acquisition in flight")

// Acquisitions is section 5.4's own repository over the acquisition table,
// scaffolded since migration 0005 and unwired until now.
type Acquisitions struct{ store *store.Store }

// NewAcquisitions builds it over an open library store.
func NewAcquisitions(opened *store.Store) Acquisitions { return Acquisitions{store: opened} }

// Create dispatches one acquisition -- refusing outright when the desired
// item already has one in flight (Invariant 15), checked and created in the
// same transaction so a concurrent Create cannot both pass the check.
func (a Acquisitions) Create(ctx context.Context, req NewAcquisition) (Acquisition, error) {
	var row librarydb.Acquisition
	err := a.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if req.DesiredItemID != nil {
			_, err := queries.GetActiveAcquisitionForItem(ctx, nullInt64(req.DesiredItemID))
			if err == nil {
				return ErrAcquisitionAlreadyInFlight
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("check in-flight: %w", err)
			}
		}

		created, err := queries.CreateAcquisition(ctx, librarydb.CreateAcquisitionParams{
			DesiredItemID: nullInt64(req.DesiredItemID), CandidateID: nullInt64(req.CandidateID),
			IndexerID: nullInt64(req.IndexerID), CandidateGuid: nullString(req.CandidateGUID),
			CandidateTitle: nullString(req.CandidateTitle), SearchTerms: nullString(req.SearchTerms),
			DownloaderID: req.DownloaderID, DownloadClientRef: req.DownloadClientRef,
			ClientCategory: nullString(req.ClientCategory), ClientFingerprint: nullString(req.ClientFingerprint),
			Protocol: req.Protocol, State: req.State,
			CreatedAt: req.CreatedAt, UpdatedAt: req.UpdatedAt,
		})
		row = created
		return err
	})
	if err != nil {
		return Acquisition{}, fmt.Errorf("library: create acquisition: %w", err)
	}
	return acquisitionFromRow(row), nil
}

// Get answers one acquisition by id.
func (a Acquisitions) Get(ctx context.Context, id int64) (Acquisition, error) {
	row, err := librarydb.New(a.store.Read()).GetAcquisition(ctx, id)
	if err != nil {
		return Acquisition{}, fmt.Errorf("library: get acquisition %d: %w", id, err)
	}
	return acquisitionFromRow(row), nil
}

// ListActive answers every non-terminal acquisition -- startup
// reconciliation's own future job (section 10.9), not built yet.
func (a Acquisitions) ListActive(ctx context.Context) ([]Acquisition, error) {
	rows, err := librarydb.New(a.store.Read()).ListActiveAcquisitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list active acquisitions: %w", err)
	}
	acquisitions := make([]Acquisition, len(rows))
	for i, row := range rows {
		acquisitions[i] = acquisitionFromRow(row)
	}
	return acquisitions, nil
}

// SetState transitions an acquisition's own state machine (section 10.3).
func (a Acquisitions) SetState(ctx context.Context, id int64, state, lastError string, at int64) error {
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetAcquisitionState(ctx, librarydb.SetAcquisitionStateParams{
			ID: id, State: state, LastError: nullString(lastError), UpdatedAt: at,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set acquisition %d state: %w", id, err)
	}
	return nil
}

func acquisitionFromRow(row librarydb.Acquisition) Acquisition {
	return Acquisition{
		ID: row.ID, DesiredItemID: int64Ptr(row.DesiredItemID), CandidateID: int64Ptr(row.CandidateID),
		IndexerID: int64Ptr(row.IndexerID), CandidateGUID: row.CandidateGuid.String,
		CandidateTitle: row.CandidateTitle.String, SearchTerms: row.SearchTerms.String,
		DownloaderID: row.DownloaderID, DownloadClientRef: row.DownloadClientRef,
		DownloadDir: row.DownloadDir.String, ClientCategory: row.ClientCategory.String,
		ClientFingerprint: row.ClientFingerprint.String, Protocol: row.Protocol, State: row.State,
		LastError: row.LastError.String, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
