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

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/platform/fsx"
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
	// Running and JobRegistry are optional: the running jobs the status bar shows, and their names.
	Running     *jobs.Running
	JobRegistry *jobs.Registry
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
	// WorkEditor backs an album's Edit dialog and the tag picker. Optional: without it they answer 503.
	WorkEditor WorkEditor
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
	// ConfigPath locates config.yml on disk. Optional: without it,
	// UpdateNetworkSettings writes only settings.db and never touches the
	// file, same as before ADR-58's precedence was revised (2026-09-12).
	ConfigPath string
	// People lists and reads people (ADR-32). Optional: without it, the people
	// endpoints answer 503.
	People PeopleReader
	// Organisations lists and reads labels, networks, studios and publishers. Optional: nil answers 503.
	Organisations OrganisationsReader
	// Series feeds the TV screens. Optional: nil answers 503.
	Series SeriesCatalog
	// Seasons searches and downloads TV seasons. Optional: nil answers 503.
	Seasons SeasonAcquirer
	// PersonRemover removes an artist. Optional: nil answers 503.
	PersonRemover PersonRemover
	// Follower follows people and organisations into libraries. Optional: nil answers 503.
	Follower Follower
	// IncompleteDiscographies feeds Discover's library-derived suggestions. Optional: nil answers 503.
	IncompleteDiscographies IncompleteDiscographies

	// Statistics reads a library's rolled-up figures. Optional: nil answers 503.
	Statistics StatisticsReader
	// Duplicates finds a library's likely copies. Optional: nil answers 503.
	Duplicates DuplicateReader
	// RecycleBinContents lists and empties the recycle bins. Optional: nil answers 503.
	RecycleBinContents RecycleBinContents
	// DuplicateResolver keeps one copy of a track and links or recycles the others. Optional: nil answers 503.
	DuplicateResolver DuplicateResolver
	// Links previews and queues the linking of a library's identical copies. Optional: nil answers 503.
	Links LinkManager

	// ArtworkChoices browses and picks provider images. Optional: nil answers 503.
	ArtworkChoices ArtworkChoices

	// ArtistActions are the artist page's bulk verbs. Optional: nil answers 503.
	ArtistActions ArtistActions
	// AlbumRefresher is an album's Refresh. Optional: nil answers 503.
	AlbumRefresher AlbumRefresher
	// ArrConnections reads a connection's indexers and download clients. Optional: nil answers 503.
	ArrConnections ArrConnections
	// BlockedContent lists and removes ADR-53 part 2's hash blocks. Optional:
	// without it, ListBlockedContent/RemoveBlockedContent answer 503.
	BlockedContent BlockedContentLister
	// Packs lists ADR-50's own detected packs (todo 4.11). Optional:
	// without it, ListPacks/GetPack answer 503.
	Packs PackLister
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
	// FingerprintRetrier re-runs what a missing AcoustID key skipped, once one
	// is entered (D12). Optional: without it the key is still stored, and a full
	// rescan still recovers those files.
	FingerprintRetrier FingerprintRetrier
	// DrainStarter starts and stops ADR-51's own drain. Optional: without
	// it, DrainRootFolder/StopDrainingRootFolder answer 503.
	DrainStarter DrainStarter
	// MoveJobs lists ADR-51 move jobs. Optional: without it, ListMoveJobs
	// answers 503.
	MoveJobs MoveJobLister
	// RenameStarter applies section 11.8's naming template to a root.
	// Optional: without it, RenameRootFolder answers 503.
	RenameStarter RenameStarter
	// NamingSchemes answers each media type's naming declaration. Optional: without it, the
	// naming endpoints answer 503.
	NamingSchemes NamingSchemes
	// Navigation resolves ADR-54's declared levels per media type. Optional:
	// without it, ListLibraries reports every library with no levels rather
	// than failing.
	Navigation NavigationProvider
	// MediaTypes says which media types have a module. Optional: without it every planned type is accepted.
	MediaTypes MediaTypeCatalog
	// LibraryTracks lists the Tracks navigation level (ADR-54). Optional:
	// without it, a work_part-entity level answers 503.
	LibraryTracks LibraryTracksReader
	// LibraryArtists lists the Artists navigation level (ADR-54). Optional:
	// without it, a person-entity level answers 503.
	LibraryArtists LibraryArtistsReader
	// DownloadRoots probes a candidate download folder for existence and
	// readability (todo 4.18, section 5.1). Optional: without it,
	// AddDownloadFolder accepts a path unprobed rather than refusing to add
	// folders at all.
	DownloadRoots DownloadFolderChecker
	// Scheduler exposes ADR-59's task registry, live settings and run-state
	// (todo 4.19). Optional: without it, the schedule endpoints answer 503.
	Scheduler Scheduler
	// CacheManager exposes ADR-60's cache budget, measurement and manual
	// clear (todo 4.19). Optional: without it, the cache endpoints answer
	// 503.
	CacheManager CacheManager
	// Discover searches a category's own metadata provider and adds a result
	// as intent (todo 4.17, ADR-56). Optional: without it, the discover
	// endpoints answer 503.
	Discover Discover

	// SystemHealth supplies the health checks that need more than the settings
	// snapshot. Optional: nil means only the settings-derived checks are run.
	SystemHealth SystemHealthSource

	// Blocklist reads and reverses section 10.11's failure memory.
	Blocklist Blocklist
	// History reads what happened to downloads: grabbed, imported, failed.
	History History
	// Queue is optional: nil answers the queue as unavailable.
	Queue Queue

	// Backups lists and takes backups (ADR-15). Restoring stays offline.
	Backups Backups

	// LogReader answers the most recent log records.
	LogReader LogReader

	// Calendar answers releases in a date window.
	Calendar CalendarReader
	ArrSync  ArrSync
	// LibraryHealth answers todo 4.8's own aggregate report (unprobed files,
	// unmatched files, incomplete works, roots near reserve, blocked hashes
	// found in the library). Optional: without it, the endpoint answers 503.
	LibraryHealth LibraryHealth
	// IndexerTester and DownloaderTester run todo 5.2's own "every connection
	// tested before it is accepted" against the real indexer/download client.
	IndexerTester IndexerTester
	// Prowlarr lists a Prowlarr's indexers to add from.
	Prowlarr         ProwlarrLister
	DownloaderTester DownloaderTester
	// CandidateSearcher is stage 5's own candidate pipeline (section 10.1/10.2).
	CandidateSearcher CandidateSearcher
	// RemoteImages serves provider pictures from the local cache. Optional: nil answers 503.
	RemoteImages RemoteImages
	// UnidentifiedTV resolves queued files of TV libraries. Optional: without it a TV provider is refused.
	UnidentifiedTV TVUnidentifiedResolver
	// TVProviders reports and tests the TV metadata providers; without it those endpoints answer 503.
	TVProviders TVProviders
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
	running *jobs.Running
	jobDefs *jobs.Registry
	events  *events.Bus
	wall    clock.Clock
	webui   http.Handler
	roots   RootChecker
	address string

	librarySize             LibrarySize
	timezone                string
	egress                  EgressReporter
	works                   WorksReader
	workDetail              WorkDetailReader
	worksMonitor            WorksMonitor
	workEditor              WorkEditor
	search                  SearchReader
	unidentified            UnidentifiedReader
	unidentifiedResolver    UnidentifiedResolver
	artwork                 ArtworkReader
	fsys                    fsx.FS
	imagesDir               string
	configPath              string
	people                  PeopleReader
	organisations           OrganisationsReader
	series                  SeriesCatalog
	seasons                 SeasonAcquirer
	personRemover           PersonRemover
	follower                Follower
	artistActions           ArtistActions
	artworkChoices          ArtworkChoices
	statistics              StatisticsReader
	duplicates              DuplicateReader
	recycleContents         RecycleBinContents
	duplicateResolver       DuplicateResolver
	links                   LinkManager
	incompleteDiscographies IncompleteDiscographies
	albumRefresher          AlbumRefresher
	arrConnections          ArrConnections
	blockedContent          BlockedContentLister
	packs                   PackLister
	removeAndBlocker        RemoveAndBlocker
	importSessions          ImportSessionLister
	importInspector         ImportInspector
	fingerprintRetrier      FingerprintRetrier
	drainStarter            DrainStarter
	moveJobs                MoveJobLister
	renameStarter           RenameStarter
	namingSchemes           NamingSchemes
	navigation              NavigationProvider
	mediaTypes              MediaTypeCatalog
	libraryTracks           LibraryTracksReader
	libraryArtists          LibraryArtistsReader
	downloadRoots           DownloadFolderChecker
	scheduler               Scheduler
	cacheManager            CacheManager
	discover                Discover
	arrSync                 ArrSync
	libraryHealth           LibraryHealth
	systemHealth            SystemHealthSource
	blocklist               Blocklist
	history                 History
	queue                   Queue
	backups                 Backups
	logReader               LogReader
	calendar                CalendarReader
	indexerTester           IndexerTester
	prowlarr                ProwlarrLister
	downloaderTester        DownloaderTester
	tvProviders             TVProviders
	remoteImages            RemoteImages
	unidentifiedTV          TVUnidentifiedResolver
	candidateSearcher       CandidateSearcher
	// effectivePort is the port this process actually bound to, resolved at
	// startup (ADR-58). The Network screen reports it beside whatever
	// settings.db now holds, since a port change written from the screen
	// needs a restart before the two agree.
	effectivePort int
	// healthPath is the one route ADR-58's IP allowlist never applies to
	// (section 20.2: a container healthcheck from a bridge network must
	// never be locked out by a LAN-only rule), computed once against
	// BaseURL rather than hard-coded against APIPrefix alone.
	healthPath string

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
		running: config.Running,
		jobDefs: config.JobRegistry,
		events:  config.Events,
		wall:    config.Clock,
		webui:   config.WebUI,
		roots:   config.Roots,

		librarySize:             config.LibrarySize,
		timezone:                config.Timezone,
		egress:                  config.Egress,
		works:                   config.Works,
		workDetail:              config.WorkDetail,
		worksMonitor:            config.WorksMonitor,
		workEditor:              config.WorkEditor,
		search:                  config.Search,
		unidentified:            config.Unidentified,
		unidentifiedResolver:    config.UnidentifiedResolver,
		artwork:                 config.Artwork,
		fsys:                    config.FS,
		imagesDir:               config.ImagesDir,
		configPath:              config.ConfigPath,
		people:                  config.People,
		organisations:           config.Organisations,
		series:                  config.Series,
		seasons:                 config.Seasons,
		personRemover:           config.PersonRemover,
		follower:                config.Follower,
		artistActions:           config.ArtistActions,
		artworkChoices:          config.ArtworkChoices,
		statistics:              config.Statistics,
		duplicates:              config.Duplicates,
		recycleContents:         config.RecycleBinContents,
		duplicateResolver:       config.DuplicateResolver,
		links:                   config.Links,
		incompleteDiscographies: config.IncompleteDiscographies,
		albumRefresher:          config.AlbumRefresher,
		arrConnections:          config.ArrConnections,
		blockedContent:          config.BlockedContent,
		packs:                   config.Packs,
		removeAndBlocker:        config.RemoveAndBlocker,
		importSessions:          config.ImportSessions,
		importInspector:         config.ImportInspector,
		fingerprintRetrier:      config.FingerprintRetrier,
		drainStarter:            config.DrainStarter,
		moveJobs:                config.MoveJobs,
		renameStarter:           config.RenameStarter,
		namingSchemes:           config.NamingSchemes,
		navigation:              config.Navigation,
		mediaTypes:              config.MediaTypes,
		libraryTracks:           config.LibraryTracks,
		libraryArtists:          config.LibraryArtists,
		downloadRoots:           config.DownloadRoots,
		scheduler:               config.Scheduler,
		cacheManager:            config.CacheManager,
		discover:                config.Discover,
		arrSync:                 config.ArrSync,
		libraryHealth:           config.LibraryHealth,
		systemHealth:            config.SystemHealth,
		blocklist:               config.Blocklist,
		history:                 config.History,
		queue:                   config.Queue,
		backups:                 config.Backups,
		logReader:               config.LogReader,
		calendar:                config.Calendar,
		indexerTester:           config.IndexerTester,
		prowlarr:                config.Prowlarr,
		downloaderTester:        config.DownloaderTester,
		candidateSearcher:       config.CandidateSearcher,
		tvProviders:             config.TVProviders,
		remoteImages:            config.RemoteImages,
		unidentifiedTV:          config.UnidentifiedTV,
		effectivePort:           config.Port,
		healthPath:              base + APIPrefix + "/health",
		address:                 net.JoinHostPort(config.Bind, fmt.Sprint(config.Port)),
	}

	router := chi.NewRouter()
	router.Use(server.recoverPanic, server.logRequest, server.ipAllowlist)
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
		api.Get("/about/license", server.GetAboutLicense)
		api.Get("/about/attribution", server.GetAboutAttribution)
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
		// Calendar clients cannot log in; the token in the URL is the credential (2026-09-19).
		api.Get("/calendar/feed.ics", server.routeCalendarFeedICS)

		// Everything else needs an identity.
		api.Group(func(secured chi.Router) {
			secured.Use(server.authenticate)
			secured.Get("/me", server.GetMe)
			secured.Patch("/me/tier", server.SetMyTier)
			secured.Get("/me/preferences", server.GetMyPreferences)
			secured.Put("/me/preferences/{key}", server.routeSetMyPreference)
			secured.Get("/me/apikeys", server.ListMyAPIKeys)
			secured.Post("/me/apikeys", server.CreateMyAPIKey)
			secured.Delete("/me/apikeys/{id}", server.routeDeleteMyAPIKey)
			secured.Get("/me/calendar-feed", server.GetMyCalendarFeed)
			secured.Post("/me/calendar-feed", server.CreateMyCalendarFeed)
			secured.Delete("/me/calendar-feed", server.DeleteMyCalendarFeed)
			secured.Get("/dashboard", server.GetDashboard)
			secured.Get("/library/health", server.GetLibraryHealth)
			secured.Get("/system/health", server.GetSystemHealth)
			secured.Get("/calendar", server.routeListCalendar)
			secured.Get("/calendar/dates", server.routeListCalendarDates)
			secured.Get("/system/logs", server.routeListLogs)
			secured.Get("/system/backups", server.ListBackups)
			secured.Post("/system/backups", server.RunBackupNow)
			secured.Get("/blocklist", server.routeListBlocklist)
			secured.Get("/history", server.routeListHistory)
			secured.Delete("/blocklist/{id}", server.routeRemoveBlocklistEntry)
			secured.Get("/queue", server.routeListQueue)
			secured.Delete("/queue/{id}", server.routeRemoveQueueItem)
			secured.Post("/queue/clients/{id}/{action}", server.routeSetQueueClientPaused)
			secured.Get("/system/egress", server.GetEgress)
			secured.Get("/system/activity", server.GetActivity)
			secured.Get("/jobs", server.routeListJobs)
			secured.Get("/works", server.routeListWorks)
			secured.Get("/works/{id}", server.routeGetWork)
			secured.Post("/works/bulk/monitor", server.BulkSetWorkMonitored)
			secured.Post("/works/bulk/edit", server.BulkEditWorks)
			secured.Post("/works/{id}/monitor", server.routeSetWorkMonitored)
			secured.Get("/works/{id}/edit", server.routeGetWorkEdit)
			secured.Put("/works/{id}/edit", server.routeSaveWorkEdit)
			secured.Get("/tags", server.ListTags)
			secured.Get("/tags/own", server.ListOwnTags)
			secured.Put("/tags/{id}", server.routeRenameTag)
			secured.Delete("/tags/{id}", server.routeDeleteTag)
			secured.Post("/works/{id}/remove", server.routeRemoveWork)
			secured.Post("/works/{id}/parts/{partId}/remove", server.routeRemovePart)
			secured.Get("/categories/{id}/quality", server.routeGetLibraryQuality)
			secured.Put("/categories/{id}/quality", server.routeSetLibraryQuality)
			secured.Get("/categories/{id}/naming", server.routeGetLibraryNaming)
			secured.Put("/categories/{id}/naming", server.routeSetLibraryNaming)
			secured.Get("/categories/{id}/file-management", server.routeGetLibraryFileManagement)
			secured.Put("/categories/{id}/file-management", server.routeSetLibraryFileManagement)
			secured.Get("/categories/{id}/tagging", server.routeGetLibraryTagging)
			secured.Put("/categories/{id}/tagging", server.routeSetLibraryTagging)
			secured.Post("/categories/{id}/naming/examples", server.routePreviewLibraryNaming)
			secured.Post("/categories/{id}/rename", server.routeRenameLibrary)
			secured.Post("/works/{id}/parts/{partId}/wanted", server.routeSetTrackWanted)
			secured.Get("/works/{id}/parts", server.routeListWorkParts)
			secured.Get("/works/{id}/artwork", server.routeGetWorkArtwork)
			secured.Put("/works/{id}/artwork", server.routeChooseWorkArtwork)
			secured.Get("/works/{id}/artwork/options", server.routeGetWorkArtworkOptions)
			secured.Get("/works/{id}/candidates", server.routeListWorkCandidates)
			secured.Post("/works/{id}/candidates/{decisionId}/send", server.routeSendCandidate)
			secured.Post("/works/{id}/candidates/{decisionId}/blocklist", server.routeBlocklistCandidate)
			secured.Post("/works/{id}/search", server.routeSearchWork)
			secured.Post("/works/{id}/grab", server.routeGrabWork)
			secured.Post("/works/{id}/refresh", server.routeRefreshWork)
			secured.Post("/works/{id}/parts/{partId}/search", server.routeSearchTrack)
			secured.Get("/series", server.routeListSeries)
			secured.Post("/series", server.AddSeries)
			secured.Get("/series/wanted", server.routeListWantedEpisodes)
			secured.Get("/images/remote", server.routeGetRemoteImage)
			secured.Get("/series/lookup", server.routeLookupSeries)
			secured.Get("/series/exclusions", server.ListSeriesExclusions)
			secured.Delete("/series/exclusions", server.routeRemoveSeriesExclusion)
			secured.Delete("/series/{id}", server.routeDeleteSeries)
			secured.Get("/series/{id}", server.routeGetSeries)
			secured.Post("/series/{id}/refresh", server.routeRefreshSeries)
			secured.Put("/series/{id}/quality", server.routeSetSeriesQuality)
			secured.Put("/series/{id}/type", server.routeSetSeriesType)
			secured.Get("/series/{id}/posters", server.routeGetSeriesPosters)
			secured.Put("/series/{id}/poster", server.routeSetSeriesPoster)
			secured.Post("/series/{id}/seasons/{season}/search", server.routeSearchSeason)
			secured.Post("/series/{id}/seasons/{season}/grab", server.routeGrabSeason)
			secured.Get("/series/{id}/seasons/{season}/candidates", server.routeListSeasonCandidates)
			secured.Post("/series/{id}/seasons/{season}/candidates/{decision_id}/send", server.routeSendSeasonCandidate)
			secured.Get("/discover/search", server.routeDiscoverSearch)
			secured.Post("/discover/add", server.DiscoverAdd)
			secured.Get("/arr/instances", server.ListArrInstances)
			secured.Post("/arr/instances", server.AddArrInstance)
			secured.Patch("/arr/instances/{id}", server.routeUpdateArrInstance)
			secured.Delete("/arr/instances/{id}", server.routeDeleteArrInstance)
			secured.Post("/arr/instances/{id}/apikey", server.routeSetArrInstanceKey)
			secured.Post("/arr/instances/{id}/sync", server.routeSyncArrInstance)
			secured.Get("/arr/instances/{id}/rootfolders", server.routeListArrInstanceRootFolders)
			secured.Get("/arr/instances/{id}/indexers", server.routeListArrInstanceIndexers)
			secured.Post("/arr/instances/{id}/indexers", server.routeUseArrInstanceIndexers)
			secured.Get("/arr/instances/{id}/downloadclients", server.routeListArrInstanceDownloadClients)
			secured.Get("/arr/imports", server.routeListArrImports)
			secured.Get("/arr/imports/{id}", server.routeGetArrImport)
			secured.Post("/arr/imports/{id}/revert", server.routeRevertArrImport)
			secured.Get("/indexers", server.ListIndexers)
			secured.Post("/indexers", server.AddIndexer)
			secured.Post("/indexers/prowlarr", server.ListProwlarrIndexers)
			secured.Patch("/indexers/{id}", server.routeUpdateIndexer)
			secured.Delete("/indexers/{id}", server.routeDeleteIndexer)
			secured.Post("/indexers/{id}/apikey", server.routeSetIndexerAPIKey)
			secured.Post("/indexers/{id}/test", server.routeTestIndexer)
			secured.Get("/downloaders", server.ListDownloaders)
			secured.Post("/downloaders", server.AddDownloader)
			secured.Post("/downloaders/test", server.TestDownloaderConfig)
			secured.Patch("/downloaders/{id}", server.routeUpdateDownloader)
			secured.Delete("/downloaders/{id}", server.routeDeleteDownloader)
			secured.Post("/downloaders/{id}/credentials", server.routeSetDownloaderCredentials)
			secured.Post("/downloaders/{id}/test", server.routeTestDownloader)
			secured.Get("/library/unidentified", server.routeListUnidentified)
			secured.Post("/library/unidentified/{mediaObjectId}/resolve", server.routeResolveUnidentified)
			secured.Post("/library/unidentified/{mediaObjectId}/not-media", server.routeMarkUnidentifiedNotMedia)
			secured.Get("/library/unidentified/{mediaObjectId}/attempts", server.routeListIdentificationAttempts)
			secured.Get("/search", server.routeSearch)
			secured.Post("/score/simulate", server.SimulateScore)
			secured.Get("/mediatypes", server.ListMediaTypes)
			secured.Get("/libraries", server.ListLibraries)
			secured.Put("/libraries/{id}/last-level", server.routeSetLibraryLastLevel)
			secured.Get("/libraries/{id}/import-lists", server.routeListImportLists)
			secured.Post("/libraries/{id}/import-lists", server.routeCreateImportList)
			secured.Patch("/import-lists/{id}", server.routeUpdateImportList)
			secured.Delete("/import-lists/{id}", server.routeDeleteImportList)
			secured.Get("/libraries/{id}/{level}", server.routeListLibraryLevel)
			secured.Get("/categories", server.ListCategories)
			secured.Post("/categories", server.CreateCategory)
			secured.Post("/categories/{id}/search", server.routeSearchCategory)
			secured.Get("/categories/{id}", server.routeGetCategory)
			secured.Get("/categories/{id}/statistics", server.routeGetCategoryStatistics)
			secured.Get("/categories/{id}/duplicates", server.routeGetCategoryDuplicates)
			secured.Post("/categories/{id}/duplicates/resolve", server.routeResolveCategoryDuplicates)
			secured.Get("/categories/{id}/links", server.routeGetCategoryLinks)
			secured.Post("/categories/{id}/links", server.routeLinkCategoryCopies)
			secured.Get("/discover/incomplete", server.routeListIncompleteDiscographies)
			secured.Patch("/categories/{id}", server.routeUpdateCategory)
			secured.Delete("/categories/{id}", server.routeDeleteCategory)
			secured.Post("/categories/{id}/rootfolders", server.routeAttachRootFolderToCategory)
			secured.Delete("/categories/{id}/rootfolders/{rootFolderId}", server.routeDetachRootFolderFromCategory)
			secured.Get("/people", server.routeListPeople)
			server.mountFollows(secured)
			secured.Get("/people/{id}/known-for", server.routeListPersonKnownFor)
			secured.Get("/organisations", server.routeListOrganisations)
			secured.Get("/organisations/{id}", server.routeGetOrganisation)
			secured.Get("/people/{id}", server.routeGetPerson)
			secured.Get("/people/{id}/artwork", server.routeGetPersonArtwork)
			secured.Put("/people/{id}/artwork", server.routeChoosePersonArtwork)
			secured.Get("/people/{id}/artwork/options", server.routeGetPersonArtworkOptions)
			secured.Post("/people/{id}/remove", server.routeRemovePerson)
			secured.Post("/people/{id}/search", server.routeSearchPersonMissing)
			secured.Post("/people/{id}/monitor-all", server.routeMonitorPersonAll)
			secured.Post("/people/{id}/refresh", server.routeRefreshPerson)
			secured.Get("/exclusions", server.ListExclusionPatterns)
			secured.Post("/exclusions", server.CreateExclusionPattern)
			secured.Patch("/exclusions/{id}", server.routeUpdateExclusionPattern)
			secured.Delete("/exclusions/{id}", server.routeDeleteExclusionPattern)
			secured.Get("/qualityprofiles", server.ListQualityProfiles)
			secured.Post("/qualityprofiles", server.CreateQualityProfile)
			secured.Patch("/qualityprofiles/{id}", server.routeUpdateQualityProfile)
			secured.Delete("/qualityprofiles/{id}", server.routeDeleteQualityProfile)
			secured.Get("/qualitytargets", server.ListQualityTargets)
			secured.Post("/qualitytargets", server.CreateQualityTarget)
			secured.Put("/qualitytargets/{id}", server.routeUpdateQualityTarget)
			secured.Delete("/qualitytargets/{id}", server.routeDeleteQualityTarget)
			secured.Get("/languageprofiles", server.ListLanguageProfiles)
			secured.Post("/languageprofiles", server.CreateLanguageProfile)
			secured.Patch("/languageprofiles/{id}", server.routeUpdateLanguageProfile)
			secured.Delete("/languageprofiles/{id}", server.routeDeleteLanguageProfile)
			secured.Get("/blocked", server.routeListBlockedContent)
			secured.Delete("/blocked/{id}", server.routeRemoveBlockedContent)
			secured.Post("/files/{mediaObjectId}/remove-block", server.routeRemoveAndBlockFile)
			secured.Post("/import/inspect", server.InspectImport)
			secured.Get("/import/sessions", server.routeListImportSessions)
			secured.Get("/import/sessions/{id}", server.routeGetImportSession)
			secured.Post("/import/sessions/{id}/category", server.routeAssignImportCategory)
			secured.Post("/import/sessions/{id}/cancel", server.routeCancelImportSession)
			secured.Post("/import/sessions/{id}/dismiss", server.routeDismissImportSession)
			secured.Post("/import/sessions/{id}/commit", server.routeCommitHeldImportUnits)
			secured.Post("/import/sessions/{id}/revert", server.routeRevertImportSession)
			secured.Get("/import/sessions/{id}/units", server.routeListImportUnits)
			secured.Post("/import/sessions/{id}/units/{unitId}/commit", server.routeCommitImportUnit)
			secured.Get("/import/settings", server.GetImportSettings)
			secured.Patch("/import/settings", server.UpdateImportSettings)
			secured.Get("/packs", server.routeListPacks)
			secured.Get("/packs/{id}", server.routeGetPack)
			secured.Get("/rootfolders", server.ListRootFolders)
			secured.Patch("/rootfolders/{id}", server.routeUpdateRootFolderPolicy)
			secured.Delete("/rootfolders/{id}", server.routeDeleteRootFolder)
			secured.Patch("/rootfolders/{id}/online", server.routeSetRootFolderOnline)
			secured.Get("/downloadfolders", server.ListDownloadFolders)
			secured.Post("/downloadfolders", server.AddDownloadFolder)
			secured.Patch("/downloadfolders/{id}", server.routeUpdateDownloadFolder)
			secured.Delete("/downloadfolders/{id}", server.routeDeleteDownloadFolder)
			secured.Get("/downloadfolders/{id}/contents", server.routeListDownloadFolderContents)
			secured.Get("/storage/browse", server.routeBrowsePath)
			secured.Get("/settings/registry", server.ListSettingDefinitions)
			secured.Get("/schedules", server.ListSchedules)
			secured.Patch("/schedules/{key}", server.routeUpdateSchedule)
			secured.Post("/schedules/{key}/run", server.routeRunSchedule)
			secured.Get("/network", server.GetNetworkSettings)
			secured.Patch("/network", server.UpdateNetworkSettings)
			secured.Get("/cache", server.GetCache)
			secured.Patch("/cache", server.UpdateCacheBudget)
			secured.Get("/recyclebin", server.GetRecycleBinLimits)
			secured.Patch("/recyclebin", server.UpdateRecycleBinLimits)
			secured.Get("/recyclebin/entries", server.ListRecycleBinEntries)
			secured.Post("/recyclebin/empty", server.EmptyRecycleBin)
			secured.Get("/priority", server.GetPrioritySettings)
			secured.Patch("/priority", server.UpdatePrioritySettings)
			secured.Get("/metadata/settings", server.GetMetadataSettings)
			secured.Patch("/metadata/settings", server.UpdateMetadataSettings)
			secured.Post("/metadata/acoustid/apikey", server.SetAcoustIDAPIKey)
			secured.Post("/metadata/theaudiodb/apikey", server.SetTheAudioDBAPIKey)
			secured.Post("/metadata/tmdb/apikey", server.SetTMDBAPIKey)
			secured.Post("/metadata/tvdb/apikey", server.SetTVDBAPIKey)
			secured.Get("/metadata/tv/providers", server.ListTVProviders)
			secured.Post("/metadata/tv/providers/test", server.TestTVProviders)
			secured.Post("/cache/clear", server.ClearCache)
			secured.Post("/rootfolders/{id}/drain", server.routeDrainRootFolder)
			secured.Delete("/rootfolders/{id}/drain", server.routeStopDrainingRootFolder)
			secured.Get("/moves", server.routeListMoveJobs)
			secured.Post("/rootfolders/{id}/rename", server.routeRenameRootFolder)
			secured.Get("/users", server.ListUsers)
			secured.Post("/users", server.CreateUser)
			secured.Get("/settings/users", server.GetUserDefaults)
			secured.Put("/settings/users", server.SetUserDefaults)
			secured.Get("/settings/backups", server.GetBackupRetention)
			secured.Put("/settings/backups", server.SetBackupRetention)
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
		case gen.ListWorksParamsSortTitle, gen.ListWorksParamsSortYear, gen.ListWorksParamsSortAdded,
			gen.ListWorksParamsSortArtist, gen.ListWorksParamsSortSearched, gen.ListWorksParamsSortImported,
			gen.ListWorksParamsSortSize, gen.ListWorksParamsSortProfile, gen.ListWorksParamsSortStatus,
			gen.ListWorksParamsSortGenre, gen.ListWorksParamsSortTag:
			sort := gen.ListWorksParamsSort(raw)
			params.Sort = &sort
		default:
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "sort must be title, year, added, artist, searched, imported, size, profile, status, genre or tag")
			return
		}
	}
	if raw := query.Get("dir"); raw != "" {
		switch gen.ListWorksParamsDir(raw) {
		case gen.ListWorksParamsDirAsc, gen.ListWorksParamsDirDesc:
			dir := gen.ListWorksParamsDir(raw)
			params.Dir = &dir
		default:
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "dir must be asc or desc")
			return
		}
	}
	if raw := query.Get("fresh"); raw != "" {
		fresh, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "fresh must be a boolean")
			return
		}
		params.Fresh = &fresh
	}
	if raw := query.Get("wanted"); raw != "" {
		wanted, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "wanted must be a boolean")
			return
		}
		params.Wanted = &wanted
	}
	if raw := query.Get("cutoff_unmet"); raw != "" {
		cutoffUnmet, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "cutoff_unmet must be a boolean")
			return
		}
		params.CutoffUnmet = &cutoffUnmet
	}
	if raw := query.Get("where"); raw != "" {
		params.Where = &raw
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
