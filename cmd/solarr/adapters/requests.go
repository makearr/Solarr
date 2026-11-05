package adapters

import (
	"context"
	"errors"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
)

// Requests serves the API's request list and decisions from the library database.
type Requests struct{ Requests library.Requests }

func (r Requests) File(ctx context.Context, request apiserver.NewRequestRecord) (apiserver.RequestRecord, error) {
	filed, err := r.Requests.File(ctx, library.NewRequest(request))
	return requestRecord(filed), err
}

func (r Requests) Get(ctx context.Context, id int64) (apiserver.RequestRecord, error) {
	found, err := r.Requests.Get(ctx, id)
	if errors.Is(err, library.ErrRequestNotFound) {
		return apiserver.RequestRecord{}, apiserver.ErrRequestNotFound
	}
	return requestRecord(found), err
}

func (r Requests) List(ctx context.Context, userID int64, state string, beforeID, limit int64) ([]apiserver.RequestRecord, error) {
	found, err := r.Requests.List(ctx, userID, state, beforeID, limit)
	records := make([]apiserver.RequestRecord, len(found))
	for index, one := range found {
		records[index] = requestRecord(one)
	}
	return records, err
}

func (r Requests) Fulfil(ctx context.Context, id, decidedBy, workID int64) (bool, error) {
	return r.Requests.Fulfil(ctx, id, decidedBy, workID)
}

func (r Requests) Decline(ctx context.Context, id, decidedBy int64, reason string) (bool, error) {
	return r.Requests.Decline(ctx, id, decidedBy, reason)
}

func requestRecord(request library.Request) apiserver.RequestRecord {
	return apiserver.RequestRecord(request)
}
