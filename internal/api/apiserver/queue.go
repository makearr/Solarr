package apiserver

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ErrQueueItemNotRemovable refuses a download already being imported, or no longer in flight.
var ErrQueueItemNotRemovable = errors.New("only a queued, downloading or finished download can be removed")

// ErrQueueItemNotFound is a download that is not in flight.
var ErrQueueItemNotFound = errors.New("no such download in the queue")

// QueueRow is one download in flight.
type QueueRow struct {
	ID            int64
	WorkID        *int64
	CategoryID    int64
	Album         string
	Artist        string
	ReleaseTitle  string
	Indexer       string
	Client        string
	State         string
	ClientState   string
	SizeBytes     int64
	SizeLeftBytes int64
	ETASeconds    *int64
	Error         string
	AddedAt       time.Time
}

// QueueClientRow is one download client's own queue.
type QueueClientRow struct {
	ID               int64
	Name             string
	Paused           bool
	SpeedBytesPerSec int64
	Items            int
	SizeLeftBytes    int64
	Error            string
}

// Queue reads the downloads in flight live from their clients, and removes one.
type Queue interface {
	List(ctx context.Context) ([]QueueRow, []QueueClientRow, error)
	Remove(ctx context.Context, id int64, blocklist bool) error
}

func (s *Server) routeListQueue(w http.ResponseWriter, r *http.Request) {
	params := gen.ListQueueParams{}
	if raw := r.URL.Query().Get("work_id"); raw != "" {
		workID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "work_id must be a number")
			return
		}
		params.WorkId = &workID
	}
	s.ListQueue(w, r, params)
}

// ListQueue answers the downloads the caller may see, and every client's own queue.
func (s *Server) ListQueue(w http.ResponseWriter, r *http.Request, params gen.ListQueueParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.queue == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the queue is not available")
		return
	}
	rows, clients, err := s.queue.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	body := gen.Queue{Clients: make([]gen.QueueClient, len(clients)), Items: []gen.QueueItem{}}
	for i, client := range clients {
		body.Clients[i] = gen.QueueClient{
			Id: client.ID, Name: client.Name, Paused: client.Paused, SpeedBytesPerSec: client.SpeedBytesPerSec,
			Items: client.Items, SizeLeftBytes: client.SizeLeftBytes, Error: strPtr(client.Error),
		}
	}
	for _, row := range rows {
		if !canReadHistory(access, row.CategoryID) {
			continue
		}
		if params.WorkId != nil && (row.WorkID == nil || *row.WorkID != *params.WorkId) {
			continue
		}
		item := gen.QueueItem{
			Id: row.ID, WorkId: row.WorkID, Album: strPtr(row.Album), Artist: strPtr(row.Artist),
			ReleaseTitle: row.ReleaseTitle, Indexer: strPtr(row.Indexer), Client: row.Client,
			State: gen.QueueItemState(row.State), EtaSeconds: row.ETASeconds, Error: strPtr(row.Error), AddedAt: row.AddedAt,
		}
		if row.ClientState != "" {
			state := gen.QueueItemClientState(row.ClientState)
			item.ClientState = &state
		}
		if row.SizeBytes > 0 {
			item.SizeBytes, item.SizeLeftBytes = &row.SizeBytes, &row.SizeLeftBytes
		}
		body.Items = append(body.Items, item)
	}
	writeJSON(w, s.logger, http.StatusOK, body)
}

func (s *Server) routeRemoveQueueItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a queue id must be a number")
		return
	}
	params := gen.RemoveQueueItemParams{}
	if raw := r.URL.Query().Get("blocklist"); raw != "" {
		blocklist, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "blocklist must be true or false")
			return
		}
		params.Blocklist = &blocklist
	}
	s.RemoveQueueItem(w, r, id, params)
}

// RemoveQueueItem takes one download out of its client; manage on its library.
func (s *Server) RemoveQueueItem(w http.ResponseWriter, r *http.Request, id int64, params gen.RemoveQueueItemParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.queue == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the queue is not available")
		return
	}
	rows, _, err := s.queue.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	var found *QueueRow
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
		}
	}
	if found == nil || !canReadHistory(access, found.CategoryID) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", ErrQueueItemNotFound.Error())
		return
	}
	if found.CategoryID == 0 {
		if !s.requireAdministrator(w, r) {
			return
		}
	} else if err := access.Require(found.CategoryID, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}

	err = s.queue.Remove(r.Context(), id, params.Blocklist != nil && *params.Blocklist)
	switch {
	case errors.Is(err, ErrQueueItemNotRemovable):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Cannot be removed", err.Error())
	case err != nil:
		writeError(w, s.logger, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
