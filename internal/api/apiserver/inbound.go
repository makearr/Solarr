package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrInboundRefused is an incoming action the instance's mode or the token's libraries do not allow.
var ErrInboundRefused = errors.New("apiserver: incoming webhook action refused")

// inboundBodyLimit and inboundPerMinute bound what one token's sender may do.
const (
	inboundBodyLimit = 64 << 10
	inboundPerMinute = 60
)

// InboundInfo is one incoming webhook as the interface shows it: never the token.
type InboundInfo struct {
	ID            int64
	Name          string
	Enabled       bool
	Actions       []string
	CategoryIDs   []int64
	MediaServerID int64
	LastUsedAt    time.Time
}

// InboundInput is what a person sets.
type InboundInput struct {
	Name          string
	Enabled       bool
	Actions       []string
	CategoryIDs   []int64
	MediaServerID int64
}

// InboundCreated is a webhook with its token, which is given once.
type InboundCreated struct {
	Info  InboundInfo
	Token string
}

// InboundRequest is what an outside tool asks for; fields not meant for the action are ignored.
type InboundRequest struct {
	Action, Path, Provider, ID string
	LibraryID, WorkID          int64
}

// InboundResult is the job an action enqueued.
type InboundResult struct {
	JobID     int64
	Duplicate bool
}

// InboundWebhooks administers incoming webhooks and runs their actions; cmd/solarr/app implements it. Authenticate says
// the same false for an unknown id, a wrong token and a disabled hook.
type InboundWebhooks interface {
	List(ctx context.Context) ([]InboundInfo, error)
	Save(ctx context.Context, id int64, input InboundInput) (InboundCreated, error)
	Rotate(ctx context.Context, id int64) (InboundCreated, error)
	Delete(ctx context.Context, id int64) error
	Authenticate(ctx context.Context, id int64, token string) (InboundInfo, bool)
	Run(ctx context.Context, hook InboundInfo, request InboundRequest) (InboundResult, error)
}

// windowLimiter allows a number of calls per key in each minute.
type windowLimiter struct {
	mutex   sync.Mutex
	now     func() time.Time
	windows map[int64]window
}

type window struct {
	start time.Time
	count int
}

func newWindowLimiter() *windowLimiter {
	return &windowLimiter{now: clock.NewSystem().Now, windows: map[int64]window{}}
}

// allow counts one call; false says the minute's allowance is spent and how long until the next minute.
func (l *windowLimiter) allow(key int64, limit int) (bool, time.Duration) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	now := l.now()
	current := l.windows[key]
	if now.Sub(current.start) >= time.Minute {
		current = window{start: now}
	}
	if current.count >= limit {
		return false, current.start.Add(time.Minute).Sub(now)
	}
	current.count++
	l.windows[key] = current
	return true, 0
}

// presentedToken reads the token from the headers, else from the query for a sender that cannot set a header.
func presentedToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	if token := r.Header.Get("X-Solarr-Token"); token != "" {
		return token
	}
	return r.URL.Query().Get("token")
}

func (s *Server) inboundOf(w http.ResponseWriter) InboundWebhooks {
	if s.inbound == nil {
		writeError(w, s.logger, fmt.Errorf("%w: incoming webhooks", settings.ErrNotFound))
		return nil
	}
	return s.inbound
}

func inboundView(info InboundInfo) gen.InboundWebhook {
	view := gen.InboundWebhook{
		Id: info.ID, Name: info.Name, Enabled: info.Enabled, MediaServerId: info.MediaServerID,
		CategoryIds: append([]int64{}, info.CategoryIDs...), Actions: []gen.InboundWebhookActions{},
	}
	for _, action := range info.Actions {
		view.Actions = append(view.Actions, gen.InboundWebhookActions(action))
	}
	if !info.LastUsedAt.IsZero() {
		view.LastUsedAt = &info.LastUsedAt
	}
	return view
}

func inboundInput(body gen.InboundWebhookWrite) InboundInput {
	input := InboundInput{Name: body.Name, Enabled: derefOr(body.Enabled, true), CategoryIDs: derefOr(body.CategoryIds, nil), MediaServerID: derefOr(body.MediaServerId, 0)}
	for _, action := range body.Actions {
		input.Actions = append(input.Actions, string(action))
	}
	return input
}

// ListInboundWebhooks answers every incoming webhook.
func (s *Server) ListInboundWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	hooks := s.inboundOf(w)
	if hooks == nil {
		return
	}
	found, err := hooks.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.InboundWebhook, len(found))
	for index, info := range found {
		views[index] = inboundView(info)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// AddInboundWebhook adds one and answers its token once.
func (s *Server) AddInboundWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	hooks := s.inboundOf(w)
	if hooks == nil {
		return
	}
	var body gen.InboundWebhookWrite
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with name and actions") {
		return
	}
	created, err := hooks.Save(r.Context(), 0, inboundInput(body))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusCreated, gen.InboundWebhookCreated{Webhook: inboundView(created.Info), Token: created.Token})
}

// UpdateInboundWebhook changes what a token may do.
func (s *Server) UpdateInboundWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	hooks := s.inboundOf(w)
	if hooks == nil {
		return
	}
	var body gen.InboundWebhookWrite
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with name and actions") {
		return
	}
	saved, err := hooks.Save(r.Context(), id, inboundInput(body))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, inboundView(saved.Info))
}

// DeleteInboundWebhook removes one.
func (s *Server) DeleteInboundWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	hooks := s.inboundOf(w)
	if hooks == nil {
		return
	}
	if err := hooks.Delete(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RotateInboundWebhookToken makes a new token.
func (s *Server) RotateInboundWebhookToken(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	hooks := s.inboundOf(w)
	if hooks == nil {
		return
	}
	created, err := hooks.Rotate(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.InboundWebhookCreated{Webhook: inboundView(created.Info), Token: created.Token})
}

// RunInboundWebhook is the one public route of the API besides the feeds: the token is the credential.
func (s *Server) RunInboundWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	notFound := func() { writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such webhook") }
	hooks := s.inbound
	if hooks == nil {
		notFound()
		return
	}
	if allowed, wait := s.inboundLimit.allow(id, inboundPerMinute); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Round(time.Second).Seconds())+1))
		writeProblem(w, s.logger, http.StatusTooManyRequests, TypeTooManyRequests, "Too many requests", "this webhook may be called 60 times a minute")
		return
	}
	hook, found := hooks.Authenticate(r.Context(), id, presentedToken(r))
	if !found {
		notFound()
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inboundBodyLimit))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeProblem(w, s.logger, http.StatusRequestEntityTooLarge, TypePayloadTooLarge, "Request too large", "the body may be 64 KiB at most")
		return
	}
	request, err := inboundRequestOf(raw)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the body must be a JSON object")
		return
	}
	if request.Action == "" {
		request.Action = r.URL.Query().Get("action")
	}
	if request.Action == "" && len(hook.Actions) == 1 {
		request.Action = hook.Actions[0]
	}
	if !slices.Contains(hook.Actions, request.Action) {
		writeProblem(w, s.logger, http.StatusForbidden, TypeForbidden, "Forbidden", "this webhook may not do that")
		return
	}
	result, err := hooks.Run(r.Context(), hook, request)
	if err != nil {
		if errors.Is(err, ErrInboundRefused) {
			writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Refused", err.Error())
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, gen.InboundActionResult{JobId: result.JobID, Duplicate: result.Duplicate})
}

// inboundRequestOf reads the fields it knows from a JSON object and ignores the rest and fields of another type, so a media server's own payload passes.
func inboundRequestOf(raw []byte) (InboundRequest, error) {
	var request InboundRequest
	if len(strings.TrimSpace(string(raw))) == 0 {
		return request, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return InboundRequest{}, err
	}
	text := func(key string) string { value, _ := fields[key].(string); return value }
	number := func(key string) int64 { value, _ := fields[key].(float64); return int64(value) }
	request.Action, request.Path, request.Provider, request.ID = text("action"), text("path"), text("provider"), text("id")
	request.LibraryID, request.WorkID = number("library_id"), number("work_id")
	return request, nil
}

// routeRunInboundWebhook reads the id itself: an id that is not a number is the same 404 as an unknown one.
func (s *Server) routeRunInboundWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such webhook")
		return
	}
	s.RunInboundWebhook(w, r, id)
}
