package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// ErrRequestNotFound reports a request id nothing was filed under.
var ErrRequestNotFound = errors.New("apiserver: request not found")

// ErrRequestDecided reports a request that was not pending any more.
var ErrRequestDecided = errors.New("apiserver: request already decided")

const (
	requestPending   = "PENDING"
	requestFulfilled = "FULFILLED"
)

// RequestRecord is one filed request; ScopeJSON holds the add as the requester asked for it.
type RequestRecord struct {
	ID, UserID, WorkID, CategoryID, DecidedBy int64
	ExternalRef, MediaType, ScopeJSON         string
	State, DeclineReason                      string
	DecidedAt, CreatedAt                      time.Time
}

// NewRequestRecord is what is filed; State PENDING waits, FULFILLED records an add that needed no decision.
type NewRequestRecord struct {
	UserID, CategoryID, WorkID int64
	ExternalRef, MediaType     string
	ScopeJSON, State           string
}

// Requests keeps what users who may ask but not add have asked for. Defined here, implemented by the caller.
type Requests interface {
	File(ctx context.Context, request NewRequestRecord) (RequestRecord, error)
	Get(ctx context.Context, id int64) (RequestRecord, error)
	List(ctx context.Context, userID int64, state string, beforeID, limit int64) ([]RequestRecord, error)
	Fulfil(ctx context.Context, id, decidedBy, workID int64) (bool, error)
	Decline(ctx context.Context, id, decidedBy int64, reason string) (bool, error)
}

// fileRequest handles an add by someone who may only request: an account with auto-approve adds at once and the add
// is recorded as fulfilled, anyone else's waits for an administrator.
func (s *Server) fileRequest(w http.ResponseWriter, r *http.Request, access authz.Access, body gen.AddWorkRequest) {
	if s.requests == nil || s.workAdder == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "requests are not available")
		return
	}
	category, err := access.Category(body.CategoryId, settings.PermissionRequest)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	scope, err := json.Marshal(body)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	filed := NewRequestRecord{
		UserID: access.UserID(), CategoryID: body.CategoryId, ExternalRef: body.Provider + ":" + body.ExternalId,
		MediaType: category.MediaType, ScopeJSON: string(scope), State: requestPending,
	}
	user, known := access.Snapshot().User(access.UserID())
	if access.Permission(body.CategoryId).AtLeast(settings.PermissionAdd) || (known && user.Limits.AutoApprove) {
		result, err := s.performAdd(r.Context(), access.UserID(), body)
		if err != nil {
			s.writeAddError(w, err)
			return
		}
		filed.State, filed.WorkID = requestFulfilled, result.WorkID
		if _, err := s.requests.File(r.Context(), filed); err != nil {
			s.logger.Warn("requests: could not record an automatic approval", "error", err)
		}
		writeJSON(w, s.logger, http.StatusOK, addWorkView(result))
		return
	}
	record, err := s.requests.File(r.Context(), filed)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.AddWorkResult{Kind: "request", RequestId: &record.ID})
}

func (s *Server) requestView(record RequestRecord) gen.RequestView {
	var scope gen.AddWorkRequest
	_ = json.Unmarshal([]byte(record.ScopeJSON), &scope)
	snapshot := s.auth.Settings.Snapshot()
	view := gen.RequestView{
		Id: record.ID, UserId: record.UserID, MediaType: record.MediaType, State: gen.RequestState(record.State),
		Title: derefOr(scope.Title, record.ExternalRef), CreatedAt: record.CreatedAt,
		Provider: &scope.Provider, ExternalId: &scope.ExternalId,
	}
	if user, found := snapshot.User(record.UserID); found {
		view.Username = user.Username
	}
	if record.CategoryID != 0 {
		view.CategoryId = &record.CategoryID
	}
	if record.WorkID != 0 {
		view.WorkId = &record.WorkID
	}
	if record.DecidedBy != 0 {
		if user, found := snapshot.User(record.DecidedBy); found {
			view.DecidedBy = &user.Username
		}
	}
	if !record.DecidedAt.IsZero() {
		view.DecidedAt = &record.DecidedAt
	}
	if record.DeclineReason != "" {
		view.DeclineReason = &record.DeclineReason
	}
	return view
}

// ListRequests answers an administrator every request and anyone else their own.
func (s *Server) ListRequests(w http.ResponseWriter, r *http.Request, params gen.ListRequestsParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.requests == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "requests are not available")
		return
	}
	userID := access.UserID()
	if access.Role() == settings.RoleAdmin {
		userID = 0
	}
	limit := min(max(derefOr(params.Limit, 100), 1), 500)
	state := ""
	if params.State != nil {
		state = string(*params.State)
	}
	records, err := s.requests.List(r.Context(), userID, state, derefOr(params.BeforeId, 0), limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.RequestPage{Items: []gen.RequestView{}}
	for index, record := range records {
		if int64(index) == limit {
			page.NextCursor = &records[index-1].ID
			break
		}
		page.Items = append(page.Items, s.requestView(record))
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

func (s *Server) routeListRequests(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListRequestsParams
	if state := query.Get("state"); state != "" {
		value := gen.RequestState(state)
		params.State = &value
	}
	if before, ok := parseInt64Query(query.Get("before_id")); ok {
		params.BeforeId = &before
	}
	if limit, ok := parseInt64Query(query.Get("limit")); ok {
		params.Limit = &limit
	}
	s.ListRequests(w, r, params)
}

// decidable loads a pending request the caller may decide: manage on its library.
func (s *Server) decidable(w http.ResponseWriter, r *http.Request) (RequestRecord, authz.Access, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return RequestRecord{}, access, false
	}
	if s.requests == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "requests are not available")
		return RequestRecord{}, access, false
	}
	id, err := parsePathID(w, s, r, "id", "an id must be a number")
	if err != nil {
		return RequestRecord{}, access, false
	}
	record, err := s.requests.Get(r.Context(), id)
	if err != nil {
		s.writeRequestError(w, err)
		return RequestRecord{}, access, false
	}
	if _, err := access.Category(record.CategoryID, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return RequestRecord{}, access, false
	}
	if record.State != requestPending {
		s.writeRequestError(w, ErrRequestDecided)
		return RequestRecord{}, access, false
	}
	return record, access, true
}

func (s *Server) writeRequestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRequestNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", err.Error())
	case errors.Is(err, ErrRequestDecided):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Already decided", err.Error())
	default:
		writeError(w, s.logger, err)
	}
}

// ApproveRequest adds what the request asked for, as the requester asked, and records it fulfilled.
func (s *Server) ApproveRequest(w http.ResponseWriter, r *http.Request, _ int64) {
	record, access, ok := s.decidable(w, r)
	if !ok {
		return
	}
	var scope gen.AddWorkRequest
	if err := json.Unmarshal([]byte(record.ScopeJSON), &scope); err != nil {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Cannot be approved", "the request no longer says what to add")
		return
	}
	result, err := s.performAdd(r.Context(), record.UserID, scope)
	if err != nil {
		s.writeAddError(w, err)
		return
	}
	changed, err := s.requests.Fulfil(r.Context(), record.ID, access.UserID(), result.WorkID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !changed {
		s.writeRequestError(w, ErrRequestDecided)
		return
	}
	s.respondRequest(w, r, record.ID)
}

// DeclineRequest turns a pending request down; the reason is shown to the requester.
func (s *Server) DeclineRequest(w http.ResponseWriter, r *http.Request, _ int64) {
	record, access, ok := s.decidable(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	changed, err := s.requests.Decline(r.Context(), record.ID, access.UserID(), body.Reason)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !changed {
		s.writeRequestError(w, ErrRequestDecided)
		return
	}
	s.respondRequest(w, r, record.ID)
}

func (s *Server) respondRequest(w http.ResponseWriter, r *http.Request, id int64) {
	record, err := s.requests.Get(r.Context(), id)
	if err != nil {
		s.writeRequestError(w, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.requestView(record))
}

func (s *Server) routeRequestDecision(decide func(http.ResponseWriter, *http.Request, int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { decide(w, r, 0) }
}

func parseInt64Query(raw string) (int64, bool) {
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil
}
