package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
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
	// ContentHash identifies what was sent wherever it is listed; empty when unknown.
	ContentHash string
	// SizeBytes is the release's size as sent; 0 when unknown.
	SizeBytes int64
	// Parts are the media refs the download covers; empty is the whole item.
	Parts []string
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
	ContentHash       string
	// SizeBytes is the release's size, what the free-space gate counts while it is on its way; 0 when unknown.
	SizeBytes int64
	// Parts are the media refs the release was judged to cover; empty is the whole item.
	Parts []string
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
			open, err := queries.ListActiveAcquisitionsForItem(ctx, nullInt64(req.DesiredItemID))
			if err != nil {
				return fmt.Errorf("check in-flight: %w", err)
			}
			for _, row := range open {
				if partsOverlap(partsOf(row.PartsJson), req.Parts) {
					return ErrAcquisitionAlreadyInFlight
				}
			}
		}
		parts, err := json.Marshal(append([]string{}, req.Parts...))
		if err != nil {
			return fmt.Errorf("encode parts: %w", err)
		}

		created, err := queries.CreateAcquisition(ctx, librarydb.CreateAcquisitionParams{
			DesiredItemID: nullInt64(req.DesiredItemID), CandidateID: nullInt64(req.CandidateID),
			IndexerID: nullInt64(req.IndexerID), CandidateGuid: nullString(req.CandidateGUID),
			CandidateTitle: nullString(req.CandidateTitle), SearchTerms: nullString(req.SearchTerms),
			DownloaderID: req.DownloaderID, DownloadClientRef: req.DownloadClientRef,
			ClientCategory: nullString(req.ClientCategory), ClientFingerprint: nullString(req.ClientFingerprint),
			Protocol: req.Protocol, State: req.State,
			CreatedAt: req.CreatedAt, UpdatedAt: req.UpdatedAt, ContentHash: nullString(req.ContentHash),
			SizeBytes: sql.NullInt64{Int64: req.SizeBytes, Valid: req.SizeBytes > 0}, PartsJson: string(parts),
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

// partsOf reads a stored parts list; an unreadable one is the whole item.
func partsOf(stored string) []string {
	var parts []string
	if err := json.Unmarshal([]byte(stored), &parts); err != nil {
		return nil
	}
	return parts
}

// partsOverlap says two downloads of one item would bring the same thing: either is the whole item, or they share a part.
func partsOverlap(a, b []string) bool {
	return len(a) == 0 || len(b) == 0 || slices.ContainsFunc(a, func(part string) bool { return slices.Contains(b, part) })
}

// OfMediaObject is the download a placed file came in with; found is false for a file a person imported by hand.
func (a Acquisitions) OfMediaObject(ctx context.Context, mediaObjectID int64) (acquisition Acquisition, found bool, err error) {
	id, err := librarydb.New(a.store.Read()).GetAcquisitionIDOfMediaObject(ctx, mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !id.Valid) {
		return Acquisition{}, false, nil
	}
	if err != nil {
		return Acquisition{}, false, fmt.Errorf("library: acquisition of media object %d: %w", mediaObjectID, err)
	}
	acquisition, err = a.Get(ctx, id.Int64)
	return acquisition, err == nil, err
}

// InFlightBytes is what a library's downloads on their way will take when they land (ADR-12's gate).
func (a Acquisitions) InFlightBytes(ctx context.Context, categoryID int64) (int64, error) {
	total, err := librarydb.New(a.store.Read()).InFlightBytesForCategory(ctx, categoryID)
	if err != nil {
		return 0, fmt.Errorf("library: in-flight bytes for category %d: %w", categoryID, err)
	}
	return total, nil
}

// LargestFailedBytes is the size of the biggest release of the item that failed since the given unix time; 0 when none did.
func (a Acquisitions) LargestFailedBytes(ctx context.Context, desiredItemID, since int64) (int64, error) {
	largest, err := librarydb.New(a.store.Read()).LargestFailedBytesForItem(ctx, librarydb.LargestFailedBytesForItemParams{
		DesiredItemID: nullInt64(&desiredItemID), UpdatedAt: since,
	})
	if err != nil {
		return 0, fmt.Errorf("library: largest failed release of item %d: %w", desiredItemID, err)
	}
	return largest, nil
}

// InFlightForItem answers the desired item's download still on its way, if it has one (Invariant 15).
func (a Acquisitions) InFlightForItem(ctx context.Context, desiredItemID int64) (Acquisition, bool, error) {
	row, err := librarydb.New(a.store.Read()).GetActiveAcquisitionForItem(ctx, nullInt64(&desiredItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return Acquisition{}, false, nil
	}
	if err != nil {
		return Acquisition{}, false, fmt.Errorf("library: in-flight acquisition for item %d: %w", desiredItemID, err)
	}
	return acquisitionFromRow(row), true, nil
}

// Conflicts says whether a download covering parts (empty: the whole item) would overlap one of the item's in flight.
func (a Acquisitions) Conflicts(ctx context.Context, desiredItemID int64, parts []string) (bool, error) {
	open, err := librarydb.New(a.store.Read()).ListActiveAcquisitionsForItem(ctx, nullInt64(&desiredItemID))
	if err != nil {
		return false, fmt.Errorf("library: in-flight acquisitions for item %d: %w", desiredItemID, err)
	}
	for _, row := range open {
		if partsOverlap(partsOf(row.PartsJson), parts) {
			return true, nil
		}
	}
	return false, nil
}

// InFlightParts answers the media refs the item's downloads on their way cover, and whether one of them is the whole item.
func (a Acquisitions) InFlightParts(ctx context.Context, desiredItemID int64) (parts map[string]bool, whole bool, err error) {
	open, err := librarydb.New(a.store.Read()).ListActiveAcquisitionsForItem(ctx, nullInt64(&desiredItemID))
	if err != nil {
		return nil, false, fmt.Errorf("library: in-flight acquisitions for item %d: %w", desiredItemID, err)
	}
	parts = map[string]bool{}
	for _, row := range open {
		covered := partsOf(row.PartsJson)
		whole = whole || len(covered) == 0
		for _, part := range covered {
			parts[part] = true
		}
	}
	return parts, whole, nil
}

// QueuedOn counts what Solarr has in one client's queue: sent and not finished downloading.
func (a Acquisitions) QueuedOn(ctx context.Context, downloaderID int64) (int, error) {
	count, err := librarydb.New(a.store.Read()).CountQueuedOnDownloader(ctx, downloaderID)
	return int(count), err
}

// OpenOnDownloader counts the downloads sent through one client and not finished (DEL-1).
func (a Acquisitions) OpenOnDownloader(ctx context.Context, downloaderID int64) (int64, error) {
	count, err := librarydb.New(a.store.Read()).CountOpenOnDownloader(ctx, downloaderID)
	if err != nil {
		return 0, fmt.Errorf("library: open downloads on client %d: %w", downloaderID, err)
	}
	return count, nil
}

// OpenFromIndexer counts the unfinished downloads and the held releases that came from one indexer (DEL-1).
func (a Acquisitions) OpenFromIndexer(ctx context.Context, indexerID int64) (open, held int64, err error) {
	queries := librarydb.New(a.store.Read())
	if open, err = queries.CountOpenFromIndexer(ctx, nullInt64(&indexerID)); err != nil {
		return 0, 0, fmt.Errorf("library: open downloads from indexer %d: %w", indexerID, err)
	}
	if held, err = queries.CountHeldFromIndexer(ctx, indexerID); err != nil {
		return 0, 0, fmt.Errorf("library: held releases from indexer %d: %w", indexerID, err)
	}
	return open, held, nil
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

// ListImported answers the imported downloads that may still sit in their client, oldest first.
func (a Acquisitions) ListImported(ctx context.Context) ([]Acquisition, error) {
	rows, err := librarydb.New(a.store.Read()).ListImportedAcquisitions(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: list imported acquisitions: %w", err)
	}
	acquisitions := make([]Acquisition, len(rows))
	for i, row := range rows {
		acquisitions[i] = acquisitionFromRow(row)
	}
	return acquisitions, nil
}

// SetPath records where the client says a download's files are, re-read on every poll.
func (a Acquisitions) SetPath(ctx context.Context, id int64, path string, at int64) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetAcquisitionPath(ctx, librarydb.SetAcquisitionPathParams{
			DownloadDir: nullString(path), UpdatedAt: at, ID: id,
		})
	})
}

// SetFingerprint records the client's own name and size for a download, when first seen.
func (a Acquisitions) SetFingerprint(ctx context.Context, id int64, fingerprint string, at int64) error {
	return a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetAcquisitionFingerprint(ctx, librarydb.SetAcquisitionFingerprintParams{
			ClientFingerprint: nullString(fingerprint), UpdatedAt: at, ID: id,
		})
	})
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

// ErrStateChanged is a transition refused because the acquisition is no longer in the state the caller read.
var ErrStateChanged = errors.New("library: the acquisition changed state since it was read")

// Transition moves an acquisition from the state the caller read to another; a row that moved meanwhile (a person
// cancelled it while a poll ran) is left alone and ErrStateChanged answered.
func (a Acquisitions) Transition(ctx context.Context, id int64, from, to, lastError string, at int64) error {
	var changed int64
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := librarydb.New(tx).TransitionAcquisitionState(ctx, librarydb.TransitionAcquisitionStateParams{
			ToState: to, LastError: nullString(lastError), UpdatedAt: at, ID: id, FromState: from,
		})
		changed = rows
		return err
	})
	if err != nil {
		return fmt.Errorf("library: move acquisition %d from %s to %s: %w", id, from, to, err)
	}
	if changed == 0 {
		return fmt.Errorf("%w: acquisition %d was not %s", ErrStateChanged, id, from)
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
		ContentHash: row.ContentHash.String, SizeBytes: row.SizeBytes.Int64, Parts: partsOf(row.PartsJson),
	}
}
