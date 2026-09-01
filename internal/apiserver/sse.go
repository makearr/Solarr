package apiserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/events"
	"github.com/makearr/MediaNexus/internal/settings"
)

// FlushInterval is how often buffered changes are sent (architecture section
// 13.4).
//
// A bulk import at 500k rows would otherwise emit an event storm that saturates
// the connection and pins the browser. Coalescing makes stream volume a
// function of the tick rate rather than of backend activity, and 250ms costs a
// latency no human perceives.
const FlushInterval = 250 * time.Millisecond

// keepAliveInterval stops a proxy closing an idle stream. A comment frame is
// used rather than a real event, so a client never has to filter heartbeats
// out of its own data.
const keepAliveInterval = 20 * time.Second

// SSE event names. "update" carries changes; "resync" says the client must
// refetch because events were lost.
const (
	sseUpdate = "update"
	sseResync = "resync"
)

// handleEvents streams changes to one client.
//
// Every event is filtered by the connection's category access *before* it is
// buffered (ADR-27): a job-progress message naming an album from a hidden
// category is a leak, and filtering at render time would already have put it in
// a buffer that something else might log.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.events == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the event stream is not running")
		return
	}

	topics, err := parseTopics(r.URL.Query().Get("topics"))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}

	// A ResponseController rather than a type assertion on http.Flusher: any
	// middleware wrapping the writer hides the optional interfaces the real one
	// implements, and the controller follows Unwrap through them.
	controller := http.NewResponseController(w)

	// The server's WriteTimeout would otherwise kill every stream after a
	// minute, which presents as a client that mysteriously reconnects on a
	// timer. A stream has no meaningful write deadline.
	if err := controller.SetWriteDeadline(time.Time{}); err != nil {
		s.logger.Debug("could not clear the write deadline for a stream", slog.Any("error", err))
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	// Nginx buffers proxied responses by default, which holds a stream until it
	// has enough bytes -- meaning nothing arrives until something else does.
	w.Header().Set("X-Accel-Buffering", "no")

	// The first flush commits the 200 and proves the connection can stream. It
	// happens before any status is written explicitly, so a writer that cannot
	// flush -- middleware wrapping away the capability, most likely -- still
	// gets a clean 500 instead of a 200 that then never sends anything and
	// leaves the client waiting until it gives up.
	if err := controller.Flush(); err != nil {
		w.Header().Del("Content-Type")
		w.Header().Del("X-Accel-Buffering")
		writeProblem(w, s.logger, http.StatusInternalServerError, TypeInternal,
			"Internal error", "streaming is not supported on this connection")
		return
	}

	subscription := s.events.Subscribe(topics, 0)
	defer subscription.Close()

	stream := &eventStream{
		writer:     w,
		controller: controller,
		clock:      s.wall,
		logger:     s.logger,
		access:     access,
		visible:    visibleCategorySet(access),
		allowAll:   allowsSystemEvents(access),
	}

	// Replay anything missed since the client's last id, before live events, so
	// ordering holds across a reconnect.
	if !stream.replay(s.events, r.Header.Get("Last-Event-ID"), subscription.Topics()) {
		return
	}
	ticker := s.newFlushTicker()
	defer ticker.Stop()
	stream.run(r, subscription, ticker.C())
}

// eventStream is one connection's state.
type eventStream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	clock      clock.Clock
	logger     *slog.Logger
	access     authz.Access
	visible    map[int64]bool
	allowAll   bool
}

// run buffers events and flushes them on a tick.
func (e *eventStream) run(r *http.Request, subscription *events.Subscription, tick <-chan time.Time) {
	// Coalescing buffer: one entry per entity, holding the latest state. A
	// hundred updates to one job in a tick become one message.
	pending := make(map[string]events.Event)
	keepAlive := e.clock.NewTicker(keepAliveInterval)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case event, open := <-subscription.Events():
			if !open {
				return
			}
			e.collect(pending, event)

		case <-tick:
			// Take everything already queued before flushing. Without this, a
			// burst that arrives between ticks is only partly coalesced --
			// whatever the select happened to consume goes out now and the rest
			// waits for the next tick, which is both more messages and a
			// staler first one.
			e.drain(subscription, pending)

			if subscription.Missed() {
				// Events were dropped for this connection. Telling the client
				// to refetch is honest; showing it stale data silently is not.
				if !e.send(sseResync, 0, map[string]string{
					"reason": "events were dropped; refetch to resynchronise",
				}) {
					return
				}
			}
			if len(pending) == 0 {
				continue
			}
			if !e.flush(pending) {
				return
			}
			clear(pending)

		case <-keepAlive.C():
			if !e.comment("keep-alive") {
				return
			}
		}
	}
}

// maySee applies category access. An event with no category is system-wide and
// follows role rather than category grants.
func (e *eventStream) maySee(event events.Event) bool {
	if event.CategoryID == 0 {
		return e.allowAll
	}
	return e.visible[event.CategoryID]
}

func (e *eventStream) flush(pending map[string]events.Event) bool {
	batch := make([]events.Event, 0, len(pending))
	var highest uint64
	for _, event := range pending {
		batch = append(batch, event)
		if event.ID > highest {
			highest = event.ID
		}
	}
	return e.send(sseUpdate, highest, map[string]any{"events": batch})
}

// replay re-sends what a reconnecting client missed, or tells it to refetch.
func (e *eventStream) replay(bus *events.Bus, lastEventID string, topics map[events.Topic]bool) bool {
	if lastEventID == "" {
		return true
	}
	since, err := strconv.ParseUint(strings.TrimSpace(lastEventID), 10, 64)
	if err != nil {
		// A client that sends nonsense gets a resync rather than an argument.
		return e.send(sseResync, 0, map[string]string{"reason": "unreadable Last-Event-ID"})
	}

	missed, complete := bus.Since(since, topics)
	if !complete {
		// The gap is older than the replay buffer. A partial history would turn
		// a lossy channel into a source of truth it was never meant to be.
		return e.send(sseResync, 0, map[string]string{
			"reason": "too far behind to replay; refetch to resynchronise",
		})
	}

	visible := make([]events.Event, 0, len(missed))
	for _, event := range missed {
		if e.maySee(event) {
			visible = append(visible, event)
		}
	}
	if len(visible) == 0 {
		return true
	}
	return e.send(sseUpdate, visible[len(visible)-1].ID, map[string]any{"events": visible})
}

func (e *eventStream) send(name string, id uint64, payload any) bool {
	body, err := json.Marshal(payload)
	if err != nil {
		e.logger.Warn("could not encode an event", slog.Any("error", err))
		return true
	}

	var frame strings.Builder
	if id > 0 {
		fmt.Fprintf(&frame, "id: %d\n", id)
	}
	fmt.Fprintf(&frame, "event: %s\ndata: %s\n\n", name, body)

	if _, err := io.WriteString(e.writer, frame.String()); err != nil {
		// The client went away mid-write. Ordinary, and not worth a log line
		// above debug.
		e.logger.Debug("event stream closed by the client", slog.Any("error", err))
		return false
	}
	if err := e.controller.Flush(); err != nil {
		return false
	}
	return true
}

func (e *eventStream) comment(text string) bool {
	if _, err := fmt.Fprintf(e.writer, ": %s\n\n", text); err != nil {
		return false
	}
	return e.controller.Flush() == nil
}

// parseTopics validates the requested topics. An unknown one is an error
// rather than a filter that matches nothing, which would look like a working
// stream that never delivers.
func parseTopics(raw string) ([]events.Topic, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var topics []events.Topic
	for _, name := range strings.Split(raw, ",") {
		topic := events.Topic(strings.TrimSpace(name))
		if !topic.Valid() {
			return nil, fmt.Errorf("unknown topic %q", name)
		}
		topics = append(topics, topic)
	}
	return topics, nil
}

func visibleCategorySet(access authz.Access) map[int64]bool {
	ids := access.VisibleCategoryIDs(settings.PermissionRead)
	visible := make(map[int64]bool, len(ids))
	for _, id := range ids {
		visible[id] = true
	}
	return visible
}

// allowsSystemEvents decides who sees events belonging to no category, on the
// same reasoning as job listings: it is a question about roles.
func allowsSystemEvents(access authz.Access) bool {
	role := access.Role()
	return access.IsSystem() || role == settings.RoleAdmin || role == settings.RoleOperator
}

// collect adds an event to the coalescing buffer, dropping what this connection
// may not see.
//
// The filter runs here, before the event is buffered, rather than at render
// time (ADR-27): an event that reached the buffer has already been kept in
// memory associated with this connection, and the point is that it never is.
func (e *eventStream) collect(pending map[string]events.Event, event events.Event) {
	if !e.maySee(event) {
		return
	}
	pending[event.Topic.String()+"|"+event.Entity] = event
}

// drain takes every event already queued, without waiting for more.
func (e *eventStream) drain(subscription *events.Subscription, pending map[string]events.Event) {
	for {
		select {
		case event, open := <-subscription.Events():
			if !open {
				return
			}
			e.collect(pending, event)
		default:
			return
		}
	}
}
