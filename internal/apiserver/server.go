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
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/jobs"
)

// APIPrefix is where every versioned endpoint lives (section 13.1).
const APIPrefix = "/api/v1"

// Timeouts that bound a request regardless of what a client does.
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
	// Roots checks a candidate library root. Without it, adding one is refused.
	Roots RootChecker
	// WebUI serves the compiled interface. A handler, not an fs.FS, so this
	// package never learns how the interface is packaged.
	WebUI http.Handler
	// LibrarySize reads library counters for the dashboard. Optional: without it
	// the figures report zero rather than the page failing.
	LibrarySize LibrarySize
	// Timezone is the zone name TZ resolved to. time.Location reports "Local"
	// once time.Local is replaced, which tells a reader nothing.
	Timezone string
	// Egress reports the public address. Optional.
	Egress EgressReporter
	// Works lists the library. Optional: without it, ListWorks answers 503.
	Works WorksReader
	// WorkDetail reads one work's full structural detail. Optional: without
	// it, GetWork answers 503.
	WorkDetail WorkDetailReader
	// WorksMonitor toggles monitored state at the album and part level
	// (section 19.6). Optional: without it, SetWorkMonitored/SetPartMonitored
	// answer 503.
	WorksMonitor WorksMonitor
	// Search ranks the library by title. Optional: without it, Search answers 503.
	Search SearchReader
	// Unidentified lists F29's manual identification queue. Optional: without
	// it, ListUnidentified answers 503.
	Unidentified UnidentifiedReader
	// UnidentifiedResolver confirms a candidate for one queued file (section
	// 21.3). Optional: without it, ResolveUnidentified answers 503.
	UnidentifiedResolver UnidentifiedResolver
	// Artwork reads a work's cached cover art. Optional: without it,
	// GetWorkArtwork answers 503.
	Artwork ArtworkReader
	// FS and ImagesDir locate the on-disk cover art cache ArtworkJob writes to.
	// Required together with Artwork -- meaningless alone.
	FS        fsx.FS
	ImagesDir string
	// People lists and reads people (ADR-32). Optional: without it, the people
	// endpoints answer 503.
	People PeopleReader
	// PeopleSubscriber subscribes a caller to a person. Optional: without it,
	// SubscribePerson answers 503.
	PeopleSubscriber PeopleSubscriber
	// BlockedContent lists and removes ADR-53 part 2's hash blocks. Optional:
	// without it, ListBlockedContent/RemoveBlockedContent answer 503.
	BlockedContent BlockedContentLister
	// RemoveAndBlocker runs ADR-53 section 3's compound action. Optional:
	// without it, RemoveAndBlockFile answers 503.
	RemoveAndBlocker RemoveAndBlocker
	// ImportSessions lists ADR-48 import sessions and their units. Optional:
	// without it, ListImportSessions/GetImportSession/ListImportUnits answer
	// 503.
	ImportSessions ImportSessionLister
	// ImportInspector enqueues ADR-48's own inspection job. Optional: without
	// it, InspectImport answers 503.
	ImportInspector ImportInspector
	// DrainStarter starts and stops ADR-51's own drain. Optional: without
	// it, DrainRootFolder/StopDrainingRootFolder answer 503.
	DrainStarter DrainStarter
	// MoveJobs lists ADR-51 move jobs. Optional: without it, ListMoveJobs
	// answers 503.
	MoveJobs MoveJobLister
	// RenameStarter applies section 11.8's naming template to a root.
	// Optional: without it, RenameRootFolder answers 503.
	RenameStarter RenameStarter
	// Navigation resolves ADR-54's declared levels per media type. Optional:
	// without it, ListLibraries reports every library with no levels rather
	// than failing.
	Navigation NavigationProvider
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

	librarySize          LibrarySize
	timezone             string
	egress               EgressReporter
	works                WorksReader
	workDetail           WorkDetailReader
	worksMonitor         WorksMonitor
	search               SearchReader
	unidentified         UnidentifiedReader
	unidentifiedResolver UnidentifiedResolver
	artwork              ArtworkReader
	fsys                 fsx.FS
	imagesDir            string
	people               PeopleReader
	peopleSubscriber     PeopleSubscriber
	blockedContent       BlockedContentLister
	removeAndBlocker     RemoveAndBlocker
	importSessions       ImportSessionLister
	importInspector      ImportInspector
	drainStarter         DrainStarter
	moveJobs             MoveJobLister
	renameStarter        RenameStarter
	navigation           NavigationProvider

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

		librarySize:          config.LibrarySize,
		timezone:             config.Timezone,
		egress:               config.Egress,
		works:                config.Works,
		workDetail:           config.WorkDetail,
		worksMonitor:         config.WorksMonitor,
		search:               config.Search,
		unidentified:         config.Unidentified,
		unidentifiedResolver: config.UnidentifiedResolver,
		artwork:              config.Artwork,
		fsys:                 config.FS,
		imagesDir:            config.ImagesDir,
		people:               config.People,
		peopleSubscriber:     config.PeopleSubscriber,
		blockedContent:       config.BlockedContent,
		removeAndBlocker:     config.RemoveAndBlocker,
		importSessions:       config.ImportSessions,
		importInspector:      config.ImportInspector,
		drainStarter:         config.DrainStarter,
		moveJobs:             config.MoveJobs,
		renameStarter:        config.RenameStarter,
		navigation:           config.Navigation,
		address:              net.JoinHostPort(config.Bind, fmt.Sprint(config.Port)),
	}

	router := chi.NewRouter()
	router.Use(server.recoverPanic, server.logRequest)
	// chi's defaults answer in plain text; section 13.1 says every error is problem+json with a
	// stable slug, and "every" includes the ones a client reaches by mistake.
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

		// Unauthenticated because before the first account exists there is nobody to authenticate as
		// (section 21.11).
		// Unauthenticated for the same reason as /setup: an instance that requires
		// no login has nobody to authenticate, and both the wizard and the running
		// interface have to be able to discover that.
		api.Get("/settings/auth", server.GetAuthPolicy)
		api.Put("/settings/auth", server.SetAuthPolicy)
		api.Get("/setup", server.GetSetupStatus)
		api.Post("/setup/admin", server.CreateFirstAdministrator)

		// Everything else needs an identity.
		api.Group(func(secured chi.Router) {
			secured.Use(server.authenticate)
			secured.Get("/me", server.GetMe)
			secured.Get("/dashboard", server.GetDashboard)
			secured.Get("/system/egress", server.GetEgress)
			secured.Get("/jobs", server.routeListJobs)
			secured.Get("/works", server.routeListWorks)
			secured.Get("/works/{id}", server.routeGetWork)
			secured.Post("/works/bulk/monitor", server.BulkSetWorkMonitored)
			secured.Post("/works/{id}/monitor", server.routeSetWorkMonitored)
			secured.Post("/works/{id}/parts/{partId}/monitor", server.routeSetPartMonitored)
			secured.Get("/works/{id}/artwork", server.routeGetWorkArtwork)
			secured.Get("/library/unidentified", server.routeListUnidentified)
			secured.Post("/library/unidentified/{mediaObjectId}/resolve", server.routeResolveUnidentified)
			secured.Get("/search", server.routeSearch)
			secured.Post("/score/simulate", server.SimulateScore)
			secured.Get("/libraries", server.ListLibraries)
			secured.Get("/categories", server.ListCategories)
			secured.Post("/categories", server.CreateCategory)
			secured.Delete("/categories/{id}", server.routeDeleteCategory)
			secured.Get("/people", server.routeListPeople)
			secured.Get("/people/{id}", server.routeGetPerson)
			secured.Post("/people/{id}/subscribe", server.routeSubscribePerson)
			secured.Get("/exclusions", server.ListExclusionPatterns)
			secured.Post("/exclusions", server.CreateExclusionPattern)
			secured.Patch("/exclusions/{id}", server.routeUpdateExclusionPattern)
			secured.Delete("/exclusions/{id}", server.routeDeleteExclusionPattern)
			secured.Get("/blocked", server.routeListBlockedContent)
			secured.Delete("/blocked/{id}", server.routeRemoveBlockedContent)
			secured.Post("/files/{mediaObjectId}/remove-block", server.routeRemoveAndBlockFile)
			secured.Post("/import/inspect", server.InspectImport)
			secured.Get("/import/sessions", server.routeListImportSessions)
			secured.Get("/import/sessions/{id}", server.routeGetImportSession)
			secured.Get("/import/sessions/{id}/units", server.routeListImportUnits)
			secured.Get("/rootfolders", server.ListRootFolders)
			secured.Post("/rootfolders/{id}/drain", server.routeDrainRootFolder)
			secured.Delete("/rootfolders/{id}/drain", server.routeStopDrainingRootFolder)
			secured.Get("/moves", server.routeListMoveJobs)
			secured.Post("/rootfolders/{id}/rename", server.routeRenameRootFolder)
			secured.Get("/users", server.ListUsers)
			secured.Post("/users", server.CreateUser)
			secured.Patch("/users/{id}", routeUser(server.UpdateUser))
			secured.Delete("/users/{id}", routeUser(server.DeleteUser))
			secured.Put("/users/{id}/access", routeUser(server.SetUserAccess))
			secured.Get("/settings/export", server.ExportSettings)
			secured.Post("/settings/import", server.ImportSettings)
			secured.Post("/rootfolders", server.AddRootFolder)
			secured.Get("/events", server.routeStreamEvents)
		})
	})

	// Registered after the API subtree, and that order is load-bearing:
	// documentation/web-interface.md.
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

// Shutdown stops accepting connections and waits for in-flight requests (section 15.2).
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

// Unwrap exposes the writer underneath, so http.ResponseController can reach Flush and
// SetWriteDeadline through this wrapper.
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

// An operation added to api/openapi.yaml stops the build until a handler exists
// (ADR-31). See documentation/api-generation.md.
var _ gen.ServerInterface = (*Server)(nil)

// Adapters build the generated parameter types; routing stays hand-written so
// the authenticated group keeps its meaning.
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

func (s *Server) routeListWorks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListWorksParams

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
	if raw := query.Get("category_id"); raw != "" {
		categoryID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "category_id must be a number")
			return
		}
		params.CategoryId = &categoryID
	}
	if raw := query.Get("sort"); raw != "" {
		switch gen.ListWorksParamsSort(raw) {
		case gen.Title, gen.Year, gen.Added:
			sort := gen.ListWorksParamsSort(raw)
			params.Sort = &sort
		default:
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "sort must be title, year or added")
			return
		}
	}
	if raw := query.Get("dir"); raw != "" {
		switch gen.ListWorksParamsDir(raw) {
		case gen.Asc, gen.Desc:
			dir := gen.ListWorksParamsDir(raw)
			params.Dir = &dir
		default:
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "dir must be asc or desc")
			return
		}
	}

	s.ListWorks(w, r, params)
}

func (s *Server) routeListUnidentified(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListUnidentifiedParams

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
	if raw := query.Get("category_id"); raw != "" {
		categoryID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "category_id must be a number")
			return
		}
		params.CategoryId = &categoryID
	}

	s.ListUnidentified(w, r, params)
}

func (s *Server) routeSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.SearchParams{Q: query.Get("q")}

	if raw := query.Get("media_type"); raw != "" {
		params.MediaType = &raw
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

	s.Search(w, r, params)
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

// derefOr reads an optional parameter: absent and zero are different.
func derefOr[T any](value *T, fallback T) T {
	if value == nil {
		return fallback
	}
	return *value
}
