package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// The states a request moves through; PENDING waits for an administrator, FULFILLED has added its work.
const (
	RequestPending   = "PENDING"
	RequestDeclined  = "DECLINED"
	RequestFulfilled = "FULFILLED"
)

// ErrRequestNotFound reports a request id nothing was filed under.
var ErrRequestNotFound = errors.New("library: request not found")

// Request is one person's ask for a work to be added to a library.
type Request struct {
	ID, UserID, WorkID, CategoryID, DecidedBy int64
	ExternalRef, MediaType, ScopeJSON         string
	State, DeclineReason                      string
	DecidedAt                                 time.Time
	CreatedAt                                 time.Time
}

// NewRequest is what is filed; State PENDING waits, FULFILLED records an add that needed no decision.
type NewRequest struct {
	UserID, CategoryID, WorkID int64
	ExternalRef, MediaType     string
	ScopeJSON, State           string
}

// Requests keeps the requests of users who may ask but not add (Seerr's model).
type Requests struct {
	store *store.Store
	clock clock.Clock
}

// NewRequests wraps an opened library database.
func NewRequests(opened *store.Store, cl clock.Clock) Requests {
	return Requests{store: opened, clock: cl}
}

// File stores a request.
func (r Requests) File(ctx context.Context, request NewRequest) (Request, error) {
	var row librarydb.Request
	err := r.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		row, err = librarydb.New(tx).CreateRequest(ctx, librarydb.CreateRequestParams{
			UserID: request.UserID, WorkID: nullID(request.WorkID), ExternalRef: sql.NullString{String: request.ExternalRef, Valid: request.ExternalRef != ""},
			MediaType: request.MediaType, CategoryID: nullID(request.CategoryID),
			ScopeJson: sql.NullString{String: request.ScopeJSON, Valid: request.ScopeJSON != ""}, State: request.State, CreatedAt: r.clock.Now().Unix(),
		})
		return err
	})
	if err != nil {
		return Request{}, fmt.Errorf("library: file request: %w", err)
	}
	return requestFromRow(row), nil
}

// Get answers one request, ErrRequestNotFound when there is none.
func (r Requests) Get(ctx context.Context, id int64) (Request, error) {
	row, err := librarydb.New(r.store.Read()).GetRequest(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrRequestNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("library: get request %d: %w", id, err)
	}
	return requestFromRow(row), nil
}

// List pages requests newest first; a zero userID or an empty state does not filter, a zero beforeID starts at the newest.
func (r Requests) List(ctx context.Context, userID int64, state string, beforeID, limit int64) ([]Request, error) {
	rows, err := librarydb.New(r.store.Read()).ListRequests(ctx, librarydb.ListRequestsParams{
		UserID: userID, State: state, BeforeID: beforeID, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: list requests: %w", err)
	}
	requests := make([]Request, len(rows))
	for index, row := range rows {
		requests[index] = requestFromRow(row)
	}
	return requests, nil
}

// Fulfil records a pending request as carried out; false says it was no longer pending.
func (r Requests) Fulfil(ctx context.Context, id, decidedBy, workID int64) (bool, error) {
	return r.decide(ctx, func(queries *librarydb.Queries, at int64) (int64, error) {
		return queries.FulfillRequest(ctx, librarydb.FulfillRequestParams{
			DecidedBy: nullID(decidedBy), DecidedAt: sql.NullInt64{Int64: at, Valid: true}, WorkID: nullID(workID), ID: id,
		})
	})
}

// Decline turns a pending request down; false says it was no longer pending.
func (r Requests) Decline(ctx context.Context, id, decidedBy int64, reason string) (bool, error) {
	return r.decide(ctx, func(queries *librarydb.Queries, at int64) (int64, error) {
		return queries.DecideRequest(ctx, librarydb.DecideRequestParams{
			State: RequestDeclined, DecidedBy: nullID(decidedBy), DecidedAt: sql.NullInt64{Int64: at, Valid: true},
			DeclineReason: sql.NullString{String: reason, Valid: reason != ""}, ID: id,
		})
	})
}

func (r Requests) decide(ctx context.Context, apply func(*librarydb.Queries, int64) (int64, error)) (bool, error) {
	var changed int64
	err := r.store.Write(ctx, store.PrioInteractive, func(_ context.Context, tx *sql.Tx) error {
		var err error
		changed, err = apply(librarydb.New(tx), r.clock.Now().Unix())
		return err
	})
	if err != nil {
		return false, fmt.Errorf("library: decide request: %w", err)
	}
	return changed > 0, nil
}

func nullID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func requestFromRow(row librarydb.Request) Request {
	request := Request{
		ID: row.ID, UserID: row.UserID, WorkID: row.WorkID.Int64, CategoryID: row.CategoryID.Int64, DecidedBy: row.DecidedBy.Int64,
		ExternalRef: row.ExternalRef.String, MediaType: row.MediaType, ScopeJSON: row.ScopeJson.String,
		State: row.State, DeclineReason: row.DeclineReason.String, CreatedAt: time.Unix(row.CreatedAt, 0).UTC(),
	}
	if row.DecidedAt.Valid {
		request.DecidedAt = time.Unix(row.DecidedAt.Int64, 0).UTC()
	}
	return request
}
