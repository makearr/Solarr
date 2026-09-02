package apiserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/events"
	"github.com/makearr/MediaNexus/internal/jobs"
)

// APIPrefix is where every versioned endpoint lives (section 13.1).
const APIPrefix = "/api/v1"

// Timeouts that bound a request regardless of what a client does. Without
// ReadHeaderTimeout a single slow client can hold a connection indefinitely.
// timeFormat is RFC 3339 in UTC everywhere in the API, so a client never has
// to guess a timezone.
const timeFormat = time.RFC3339

const (
	readHeaderTimeout = 10 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 30 * time.Second
)

// Config wires the server.
type Config struct {
	Bind string
	Port int
	// BaseURL lets the API sit behind a reverse proxy on a subpath.
	BaseURL string
	Build   BuildInfo
	Checker Checker
	Auth    AuthConfig
	// Jobs is optional: a CLI command that only serves health has no queue.
	Jobs *jobs.Queue
	// Events is optional for the same reason.
	Events *events.Bus
	// Clock drives the SSE flush tick. Injected so a test can advance it
	// rather than sleeping (ADR-17).
	Clock  clock.Clock
	Logger *slog.Logger
	// Roots proves a candidate library root is usable before it is accepted.
	// Optional: without it, adding a root folder is refused rather than
	// accepted unchecked -- an unverified root is the failure this prevents.
	Roots RootChecker
	// WebUI serves the compiled interface. Optional: a CLI subcommand that only
	// needs the API passes nothing and every path outside the API answers 404.
	//
	// It is a handler rather than an fs.FS so apiserver does not have to know how
	// the interface is packaged -- that belongs to internal/webui.
	WebUI http.Handler
}

// Server is the HTTP listener. Everything stays an http.Handler (ADR-31), so
// nothing here owns the shape of the code.
type Server struct {
	http    *http.Server
	router  chi.Router
	logger  *slog.Logger
	build   BuildInfo
	checker Checker
	auth    AuthConfig
	jobs    *jobs.Queue
	events  *events.Bus
	wall    clock.Clock
	webui   http.Handler
	roots   RootChecker
	address string

	// ready gates /ready independently of the checks themselves, so the answer
	// is false while starting and false again the moment shutdown begins.
	ready atomic.Bool
}

// New builds a server. It does not listen until Start.
func New(config Config) (*Server, error) {
	if config.Checker == nil {
		return nil, errors.New("apiserver: a Checker is required")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.Clock == nil {
		config.Clock = clock.NewSystem()
	}
	base := strings.TrimSuffix(config.BaseURL, "/")

	server := &Server{
		logger:  config.Logger,
		build:   config.Build,
		checker: config.Checker,
		auth:    config.Auth,
		jobs:    config.Jobs,
		events:  config.Events,
		wall:    config.Clock,
		webui:   config.WebUI,
		roots:   config.Roots,
		address: net.JoinHostPort(config.Bind, fmt.Sprint(config.Port)),
	}

	router := chi.NewRouter()
	router.Use(server.recoverPanic, server.logRequest)
	// chi's defaults answer in plain text; section 13.1 says every error is
	// problem+json with a stable slug, and "every" includes the ones a client
	// reaches by mistake.
	router.NotFound(server.handleNotFound)
	router.MethodNotAllowed(server.handleMethodNotAllowed)
	router.Route(base+APIPrefix, func(api chi.Router) {
		// Unauthenticated by design: an orchestrator has no credentials, and
		// AGPL section 13 obliges About to be reachable without logging in.
		api.Get("/health", server.GetHealth)
		api.Get("/ready", server.GetReady)
		api.Get("/about", server.GetAbout)
		api.Post("/auth/login", server.Login)
		api.Post("/auth/logout", server.Logout)

		// Unauthenticated because before the first account exists there is
		// nobody to authenticate as. Creating that account is refused once one
		// exists, which is what closes the window (section 21.11).
		api.Get("/setup", server.GetSetupStatus)
		api.Post("/setup/admin", server.CreateFirstAdministrator)

		// Everything else needs an identity. Grouping rather than per-route
		// middleware means a new endpoint is authenticated by default: adding
		// one outside this group is a visible act.
		api.Group(func(secured chi.Router) {
			secured.Use(server.authenticate)
			secured.Get("/me", server.GetMe)
			secured.Get("/jobs", server.routeListJobs)
			secured.Get("/rootfolders", server.ListRootFolders)
			secured.Get("/settings/export", server.ExportSettings)
			secured.Post("/settings/import", server.ImportSettings)
			secured.Post("/rootfolders", server.AddRootFolder)
			secured.Get("/events", server.routeStreamEvents)
		})
	})

	// The interface takes everything the API did not claim.
	//
	// Registered *after* the API subtree, which is what keeps a mistyped
	// /api/v1 path answering problem+json: chi gives that subtree the NotFound
	// handler in force when Route was called, so this later reassignment applies
	// only outside it. Move this above the Route call and every wrong API path
	// starts returning the application shell with status 200.
	// TestTheWebUINeverSwallowsAnAPIPath holds that line.
	if server.webui != nil {
		router.NotFound(server.webui.ServeHTTP)
	}

	server.router = router
	server.http = &http.Server{
		Addr:              server.address,
		Handler:           router,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(config.Logger.Handler(), slog.LevelWarn),
	}
	return server, nil
}

// Handler exposes the router, so tests can drive it without a socket.
func (s *Server) Handler() http.Handler { return s.router }

// Address reports what the server listens on.
func (s *Server) Address() string { return s.address }

// MarkReady flips /ready to 200. Called once startup has finished every step
// that must precede serving traffic (section 15.1).
func (s *Server) MarkReady() { s.ready.Store(true) }

// Serve listens and blocks until the server stops. A closed server returns nil
// rather than http.ErrServerClosed, because an orderly stop is not an error.
func (s *Server) Serve(listener net.Listener) error {
	if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("apiserver: serve: %w", err)
	}
	return nil
}

// Listen opens the socket. Separated from Serve so a caller can report a bound
// address, or fail on a port clash, before anything else starts.
func (s *Server) Listen() (net.Listener, error) {
	listener, err := net.Listen("tcp", s.address)
	if err != nil {
		return nil, fmt.Errorf("apiserver: listen on %s: %w", s.address, err)
	}
	return listener, nil
}

// Shutdown stops accepting connections and waits for in-flight requests.
//
// /ready goes false first, so an orchestrator stops sending work before the
// process stops accepting it (section 15.2 step 1).
func (s *Server) Shutdown(ctx context.Context) error {
	s.ready.Store(false)

	stopping, cancel := context.WithTimeout(ctx, shutdownGrace)
	defer cancel()

	if err := s.http.Shutdown(stopping); err != nil {
		return fmt.Errorf("apiserver: shutdown: %w", err)
	}
	return nil
}

// recoverPanic turns a panicking handler into a 500 rather than a dead process.
func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("handler panicked",
					slog.String("path", r.URL.Path), slog.Any("panic", recovered))
				writeProblem(w, s.logger, http.StatusInternalServerError, TypeInternal,
					"Internal error", "the request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logRequest records one line per request at debug, so an ordinary run is not
// drowned in traffic but a diagnosis has it.
func (s *Server) logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.logger.Debug("request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", recorder.status))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.written {
		r.status, r.written = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap exposes the writer underneath, so http.ResponseController can reach
// Flush and SetWriteDeadline through this wrapper.
//
// Without it, wrapping the writer to record a status code silently removes
// every optional interface the real one implements -- embedding
// http.ResponseWriter promotes only that interface's methods, and Flush is not
// one of them. The symptom is that streaming stops working, with nothing in the
// logs and no obvious connection to a middleware that only counts statuses.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
		"Not found", fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
}

func (s *Server) handleMethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	writeProblem(w, s.logger, http.StatusMethodNotAllowed, TypeMethodNotAllowed,
		"Method not allowed", fmt.Sprintf("%s is not allowed on %s", r.Method, r.URL.Path))
}

// clock returns the SSE flush tick. A separate method so the ticker's lifetime
// belongs to the connection rather than the server.
func (s *Server) newFlushTicker() clock.Ticker { return s.wall.NewTicker(FlushInterval) }

// The server implements the interface generated from api/openapi.yaml (ADR-31).
//
// This is the whole point of generating it: add an operation to the spec and
// this stops compiling until a handler exists. It catches a missing handler,
// a renamed operation and a changed parameter type. It cannot catch a route
// registered here that the spec does not describe -- TestRoutesMatchTheSpec
// covers that direction.
var _ gen.ServerInterface = (*Server)(nil)

// The two operations with parameters are reached through an adapter that turns
// the query string into the generated parameter type. Routing stays hand-written
// so that the authenticated group above keeps its meaning: a route added outside
// it is a visible act, where generated routing would register everything flat and
// make "is this authenticated?" a question you have to go and answer.
func (s *Server) routeListJobs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListJobsParams

	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	if raw := query.Get("state"); raw != "" {
		state := gen.JobState(raw)
		params.State = &state
	}

	s.ListJobs(w, r, params)
}

func (s *Server) routeStreamEvents(w http.ResponseWriter, r *http.Request) {
	var params gen.StreamEventsParams

	if raw := r.URL.Query().Get("topics"); strings.TrimSpace(raw) != "" {
		requested := make([]gen.EventTopic, 0, 1)
		for _, name := range strings.Split(raw, ",") {
			requested = append(requested, gen.EventTopic(strings.TrimSpace(name)))
		}
		params.Topics = &requested
	}
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		params.LastEventID = &raw
	}

	s.StreamEvents(w, r, params)
}

// derefOr reads through an optional generated parameter. Every query parameter
// is a pointer because absent and zero are different -- ?limit=0 is a request
// for none, no limit at all is a request for the default.
func derefOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}
