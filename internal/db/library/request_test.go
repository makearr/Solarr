package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func createRequest(t *testing.T, opened *store.Store, userID int64, mediaType string) librarydb.Request {
	t.Helper()
	var row librarydb.Request
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		created, err := q.CreateRequest(ctx, librarydb.CreateRequestParams{
			UserID: userID, MediaType: mediaType, State: "PENDING", CreatedAt: 1,
		})
		row = created
		return err
	})
	return row
}

func TestRequest_CreateAndGet(t *testing.T) {
	opened, queries := newLibrary(t)
	created := createRequest(t, opened, 7, "movie")

	found, err := queries.GetRequest(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if found.UserID != 7 || found.State != "PENDING" {
		t.Errorf("got %+v, want user 7, state PENDING", found)
	}
}

// An approval queue works through its backlog in order, oldest first, and a page
// boundary inside two requests sharing a moment must not drop one.
func TestRequest_ListsPendingOldestFirst(t *testing.T) {
	opened, queries := newLibrary(t)
	first := createRequest(t, opened, 1, "movie")
	second := createRequest(t, opened, 2, "tv")

	rows, err := queries.ListPendingRequests(t.Context(), librarydb.ListPendingRequestsParams{
		AfterID: 0, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPendingRequests: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != first.ID || rows[1].ID != second.ID {
		t.Errorf("got %+v, want [%d %d]", rows, first.ID, second.ID)
	}
}

// A decided request must not still show up in the pending queue.
func TestRequest_DecidedRequestLeavesThePendingQueue(t *testing.T) {
	opened, queries := newLibrary(t)
	req := createRequest(t, opened, 1, "movie")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		_, err := q.DecideRequest(ctx, librarydb.DecideRequestParams{
			State: "APPROVED", DecidedBy: sql.NullInt64{Int64: 9, Valid: true},
			DecidedAt: sql.NullInt64{Int64: 2, Valid: true}, ID: req.ID,
		})
		return err
	})

	rows, err := queries.ListPendingRequests(t.Context(), librarydb.ListPendingRequestsParams{
		AfterID: 0, PageSize: 10,
	})
	if err != nil {
		t.Fatalf("ListPendingRequests: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d still-pending requests, want 0", len(rows))
	}
}

// A race between two admins, or a retried decision, must not silently overwrite
// who decided a request and when -- the second attempt has to report that nothing
// changed.
func TestRequest_CannotBeDecidedTwice(t *testing.T) {
	opened, queries := newLibrary(t)
	req := createRequest(t, opened, 1, "movie")

	decide := func(by int64) int64 {
		t.Helper()
		var rows int64
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			count, err := q.DecideRequest(ctx, librarydb.DecideRequestParams{
				State: "APPROVED", DecidedBy: sql.NullInt64{Int64: by, Valid: true},
				DecidedAt: sql.NullInt64{Int64: 2, Valid: true}, ID: req.ID,
			})
			rows = count
			return err
		})
		return rows
	}

	if got := decide(9); got != 1 {
		t.Fatalf("first decision affected %d rows, want 1", got)
	}
	if got := decide(10); got != 0 {
		t.Fatalf("second decision affected %d rows, want 0: it must not be able to re-decide", got)
	}

	found, err := queries.GetRequest(t.Context(), req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if found.DecidedBy.Int64 != 9 {
		t.Errorf("decided_by = %d, want 9: the second, no-op decision must not have overwritten it", found.DecidedBy.Int64)
	}
}

// Fulfilling only makes sense from APPROVED -- a still-pending request has nothing
// to fulfil yet.
func TestRequest_CannotBeFulfilledWithoutApproval(t *testing.T) {
	opened, queries := newLibrary(t)
	req := createRequest(t, opened, 1, "movie")

	var rows int64
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		count, err := q.MarkRequestFulfilled(ctx, req.ID)
		rows = count
		return err
	})
	if rows != 0 {
		t.Fatalf("fulfilled a still-PENDING request, affected %d rows, want 0", rows)
	}

	found, err := queries.GetRequest(t.Context(), req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if found.State != "PENDING" {
		t.Errorf("state = %q, want PENDING unchanged", found.State)
	}
}
