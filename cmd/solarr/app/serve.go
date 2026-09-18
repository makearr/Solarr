// Package app is the server process: startup order, job wiring and shutdown (architecture section 15).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/api/health"
	"github.com/makearr/Solarr/internal/api/webui"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/recovery"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/jobs/cachebudget"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/moving/drainjob"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/egress"
	"github.com/makearr/Solarr/internal/platform/events"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/privilege"
	"github.com/makearr/Solarr/internal/platform/secrets"
	"github.com/makearr/Solarr/migrations"
)

// Build is the version metadata main injects at link time (documentation/versioning.md).
type Build struct {
	Version, Milestone, Commit, BuildDate, SourceURL, License string
}

// process is everything the server owns, so shutdown has one place to look.
type process struct {
	build    Build
	config   config.Config
	fsys     fsx.FS
	logger   *slog.Logger
	settings *store.Store
	jobs     *store.Store
	library  *store.Store
	metadata *store.Store
	// configPath is where config.yml lives, for the write-back ADR-58's
	// revised precedence needs (2026-09-12): config.yml now outranks
	// settings.db, so the Network screen has to be able to update it.
	configPath string
	queue      *jobs.Queue
	cache      *settings.Cache
	bus        *events.Bus
	dispatcher *jobs.Dispatcher
	server     *apiserver.Server
	timezone   string
	// defaultQualityProfileID is ensureDefaultQualityProfile's fallback,
	// used when a category has none of its own configured (todo 4.4).
	defaultQualityProfileID int64
	// taskRegistry is ADR-59's own registry of every periodic action;
	// taskRunners is one live TaskRunner per registered task, keyed by Key,
	// so the API's Run now can reach the exact runner already driving that
	// task's own mutex (todo 4.19) rather than starting a second, unrelated
	// enqueue path.
	taskRegistry   *jobs.TaskRegistry
	scheduleStates *jobs.ScheduleStates
	taskRunners    map[string]*jobs.TaskRunner
	// settingsRegistry is ADR-26's own registry of every scalar setting
	// (todo 4.14), built once alongside cache -- write paths refuse a key
	// this does not know about (settings.ErrSettingNotRegistered).
	settingsRegistry *settings.Registry
	// cacheSweep is ADR-60's own measure/enforce/clear (todo 4.19), built once
	// in registerCacheJob and reused by the API adapter in startServer.
	cacheSweep *cachebudget.Sweep
	// effectivePort is ADR-58's own resolution, computed once here and shown
	// by the Network screen as "in effect" -- a port change needs a restart.
	effectivePort int
	// musicBrainzOnce builds musicBrainzShared once: every job's provider must
	// draw on the same 1 req/s budget (ADR-13), see musicBrainzBudget.
	musicBrainzOnce   sync.Once
	musicBrainzShared musicBrainzBudget
}

// Serve is the startup sequence of architecture section 15.1, in order.
// Refusing to start is preferred over starting with silent data loss.
func Serve(ctx context.Context, configPath string, out io.Writer, build Build) error {
	fsys := fsx.NewOS()

	// Before anything logs a timestamp or reads a directory.
	timezone := privilege.ApplyTimezone(os.LookupEnv, out)

	identity, err := privilege.Resolve(os.LookupEnv)
	if err != nil {
		return err
	}
	privilege.WarnIfUnapplicable(identity, out)

	loaded, err := loadOrCreateConfig(fsys, configPath, out)
	if err != nil {
		return err
	}

	logger := newLogger(loaded)
	logger.Info("timezone", "tz", timezone)

	// Between loading config and checking directories, both deliberately:
	// documentation/container-runtime.md.
	if _, err := privilege.Apply(fsys, identity, loaded.ManagedDirectories(), logger); err != nil {
		return err
	}

	if err := config.CheckDirectories(fsys, loaded).OrNil(); err != nil {
		return err
	}

	running := &process{
		build: build, config: loaded, fsys: fsys, logger: logger, timezone: timezone, configPath: configPath,
	}
	defer running.close()

	if err := running.openDatabases(ctx); err != nil {
		return err
	}
	if err := running.ensureDefaultQualityProfile(ctx); err != nil {
		return err
	}
	if err := running.startJobs(ctx); err != nil {
		return err
	}
	return running.startServer(ctx)
}

// loadOrCreateConfig writes a default config.yml on a first run, then loads it.
func loadOrCreateConfig(fsys fsx.FS, path string, out io.Writer) (config.Config, error) {
	loaded, err := config.Load(fsys, path)
	if !errors.Is(err, config.ErrNotFound) {
		return loaded, err
	}

	if _, writeErr := fmt.Fprintf(out, "no config.yml at %s; writing the defaults\n", path); writeErr != nil {
		return config.Config{}, writeErr
	}
	return config.Create(fsys, path, config.GenerateKey, config.OSEnv())
}

func newLogger(loaded config.Config) *slog.Logger {
	options := &slog.HandlerOptions{Level: slogLevel(loaded.Logging.Level)}
	if loaded.Logging.Format == config.FormatText {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}

func slogLevel(level config.LogLevel) slog.Level {
	switch level {
	case config.LevelDebug:
		return slog.LevelDebug
	case config.LevelWarn:
		return slog.LevelWarn
	case config.LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (p *process) openDatabases(ctx context.Context) error {
	wall := clock.NewSystem()
	backupDir := p.config.Paths.BackupDir

	// Section 15.1 step 2: each database is checked before it is used, and a
	// corrupt one is replaced from a backup rather than started from (ADR-15).
	var restored []store.RecoveryReport
	for _, target := range []struct {
		profile databaseProfile
		into    **store.Store
		owner   func() (store.Owner, error)
	}{
		{settingsProfile, &p.settings, migrations.Settings},
		{jobsProfile, &p.jobs, migrations.Jobs},
		{libraryProfile, &p.library, migrations.Library},
	} {
		opened, report, err := store.OpenChecked(ctx, wall, p.storeConfig(target.profile),
			store.RecoveryOptions{BackupDir: backupDir})
		if err != nil {
			return err
		}
		*target.into = opened
		if report.Recovered {
			restored = append(restored, report)
			p.logRestore(report)
		}

		// Migrations back up first and refuse to run without somewhere to back
		// up to (Invariant 7).
		owner, err := target.owner()
		if err != nil {
			return err
		}
		if err := store.Migrate(ctx, opened, backupDir, owner); err != nil {
			return err
		}
	}

	cache, err := settings.NewCache(ctx, p.settings)
	if err != nil {
		return err
	}
	p.cache = cache
	p.settingsRegistry = settings.NewDefaultRegistry()

	// Equips the cache to seal/open an *Arr instance's own API key
	// (todo 4.13): the same encryption key authConfig decodes for session
	// signing, config.Create already having generated one on first run
	// (Invariant 61 -- validated at startup, so this decode cannot fail here
	// having already succeeded once during config load).
	encryptionKey, err := config.DecodeKey(p.config.Security.EncryptionKey)
	if err != nil {
		return err
	}
	cipher, err := secrets.New(encryptionKey)
	if err != nil {
		return err
	}
	cache.EnableSecrets(cipher)

	// cache/metadata.db (ADR-2) is disposable -- unlike the three above, a
	// corrupt copy is the operator's to delete, not OpenChecked's to restore
	// from a backup that was never rotated for it in the first place.
	metadataStore, err := store.Open(ctx, wall, p.storeConfig(metadataProfile))
	if err != nil {
		return err
	}
	p.metadata = metadataStore
	metadataOwner, err := migrations.Metadata()
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, metadataStore, backupDir, metadataOwner); err != nil {
		return err
	}

	return p.reconcileRestore(ctx, wall, restored)
}

// logRestore says loudly what was replaced with what. Recovery is never silent
// (ADR-15), and this is the line an operator finds first.
func (p *process) logRestore(report store.RecoveryReport) {
	p.logger.Warn("database restored from backup",
		slog.String("database", report.Database),
		slog.String("backup", report.From),
		slog.Time("backup_taken_at", report.BackupAt),
		slog.String("damaged_database_kept_at", report.CorruptPath),
		slog.Any("backups_rejected_as_corrupt", report.Rejected))
}

// reconcileRestore runs ADR-15's post-restore pass. Section 15.1 puts it at step 3,
// before migrations; it runs after them here because it queries both databases at
// the current schema and needs settings.db's configuration to find orphaned
// references. It is still well before workers start, which is what ADR-15 requires.
func (p *process) reconcileRestore(ctx context.Context, cl clock.Clock, restored []store.RecoveryReport) error {
	if len(restored) == 0 {
		return nil
	}

	snapshot := p.cache.Snapshot()
	known := recovery.Known{}
	for _, category := range snapshot.Categories() {
		known.CategoryIDs = append(known.CategoryIDs, category.ID)
	}
	for _, root := range snapshot.RootFolders() {
		known.RootFolderIDs = append(known.RootFolderIDs, root.ID)
	}

	report, err := recovery.Reconcile(ctx, cl, p.library, p.jobs, known, restored)
	if err != nil {
		return err
	}
	p.logger.Warn("post-restore reconciliation",
		slog.Int64("jobs_failed", report.JobsFailed),
		slog.Int64("desired_items_marked_dirty", report.DesiredItemsDirtied),
		slog.Any("orphaned_references", report.Orphans))
	return nil
}

// databaseProfile sizes one database for what it holds. SQLite's cache is per
// connection, so a figure meant for the library, applied to all three, is a ceiling
// several times what anyone asked for (documentation/database-layout.md).
type databaseProfile struct {
	name         string
	readPoolSize int
	// cacheSizeKB is zero for the database config.yml sizes, which is the library.
	cacheSizeKB int
	// inCacheDir puts the file under Paths.CacheDir instead of Paths.DBDir --
	// ADR-2 keeps the disposable cache directory separate from the authoritative
	// databases on purpose.
	inCacheDir bool
}

var (
	// The library is the only one at ADR-22 scale and the only one worth tuning.
	libraryProfile = databaseProfile{name: "library", readPoolSize: 4}
	// Small, hot and written constantly, but never queried across 500k rows.
	jobsProfile = databaseProfile{name: "jobs", readPoolSize: 2, cacheSizeKB: 8192}
	// Fully cached in Go by ADR-25, so its page cache serves startup and little else.
	settingsProfile = databaseProfile{name: "settings", readPoolSize: 1, cacheSizeKB: 2048}
	// Disposable (ADR-2); small on purpose, the same as settings.
	metadataProfile = databaseProfile{name: "metadata", readPoolSize: 1, cacheSizeKB: 2048, inCacheDir: true}
)

func (p *process) storeConfig(profile databaseProfile) store.Config {
	cacheSizeKB := profile.cacheSizeKB
	if cacheSizeKB == 0 {
		cacheSizeKB = p.config.Database.CacheSizeKB
	}
	directory := p.config.Paths.DBDir
	if profile.inCacheDir {
		directory = p.config.Paths.CacheDir
	}
	return store.Config{
		Name:         profile.name,
		Path:         fmt.Sprintf("%s/%s.db", directory, profile.name),
		ReadPoolSize: profile.readPoolSize,
		BusyTimeout:  p.config.Database.BusyTimeout.AsDuration(),
		PageSize:     p.config.Database.PageSize,
		CacheSizeKB:  cacheSizeKB,
		WriteWarn:    p.config.Database.WriteWarn.AsDuration(),
		WriteHard:    p.config.Database.WriteHard.AsDuration(),
		Logger:       p.logger,
	}
}

func (p *process) startJobs(ctx context.Context) error {
	wall := clock.NewSystem()
	p.bus = events.NewBus(wall)
	registry := jobs.NewRegistry()
	queue := jobs.NewQueue(p.jobs, wall, registry, nil)
	p.queue = queue

	// The lease owner must be unique per process run, or the reaper cannot tell
	// a stale lease from a live one after a restart.
	owner := fmt.Sprintf("%s-%d-%d", hostname(), os.Getpid(), time.Now().UnixNano())

	executor := jobs.NewExecutor(queue, wall, jobs.ExecutorConfig{
		Owner:  owner,
		Mode:   jobs.Mode(p.config.Mode),
		Logger: p.logger,
	})
	if err := p.registerScanJob(ctx, registry, executor); err != nil {
		return err
	}
	if err := p.registerEnrichJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerArtworkJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerCategoryCountsJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerFreshCountsJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerProbeJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerCapacityJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerSubscriptionExpandJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerImportJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerMoverJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerSearchCategoryJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerCacheJob(registry, executor); err != nil {
		return err
	}
	p.dispatcher = jobs.NewDispatcher(queue, wall, executor, jobs.DispatcherConfig{
		Sizes: []jobs.PoolSize{
			{Pool: jobs.PoolSearch, Capacity: p.config.Workers.Search},
			{Pool: jobs.PoolAcquire, Capacity: p.config.Workers.Acquire},
			{Pool: jobs.PoolMetadata, Capacity: p.config.Workers.Metadata},
			{Pool: jobs.PoolImport, Capacity: jobs.ImportCapacity},
			{Pool: jobs.PoolMaintenance, Capacity: 1},
		},
		Logger: p.logger,
	})
	return nil
}

// resolvedServerPort applies ADR-58's revised order (2026-09-12, on the
// user's own word): env, then config.yml, then settings.db. config.yml's
// own server.port is never absent (the template always sets one, default
// 5454), so in practice this makes it the value that wins whenever it
// disagrees with settings.db -- UpdateNetworkSettings writes both together
// now, so the two should agree except right after an operator hand-edits
// config.yml directly. settings.db stays checked, for a setting that
// someday has a config.yml side that can genuinely be absent.
func (p *process) resolvedServerPort() (port int, source string) {
	if _, envSet := os.LookupEnv(config.EnvName("server.port")); envSet {
		return p.config.Server.Port, "env"
	}
	if p.config.Server.Port != 0 {
		return p.config.Server.Port, "config.yml"
	}
	if stored := p.cache.Snapshot().NetworkSettings().Port; stored != 0 {
		return stored, "settings.db"
	}
	return p.config.Server.Port, "config.yml"
}

func (p *process) startServer(ctx context.Context) error {
	authConfig, err := p.authConfig()
	if err != nil {
		return err
	}
	var portSource string
	p.effectivePort, portSource = p.resolvedServerPort()

	p.reportRootFolders()
	// Each is also a registered task (todo 4.19): this first call is what
	// makes the library browsable immediately on a fresh start rather than
	// waiting for the task's own first scheduled tick, which for a 30-minute
	// cadence could otherwise be up to 30 minutes away. Errors are already
	// logged inside each function; discarded here since there is nothing
	// further for a one-off startup call to do with them.
	_, _ = p.enqueueLibraryScans(ctx)
	_, _ = p.enqueueEnrichment(ctx)
	_, _ = p.enqueueSubscriptionExpand(ctx)
	_, _ = p.enqueueArtwork(ctx)
	_, _ = p.enqueueCategoryCounts(ctx)
	_, _ = p.enqueueFreshCounts(ctx)
	_, _ = p.enqueueProbe(ctx)
	_, _ = p.enqueueCapacity(ctx)
	_, _ = p.enqueueCacheMaintenance(ctx)
	p.startSchedulers(ctx)
	if err := p.seedAuthPolicy(ctx); err != nil {
		return err
	}
	p.announceOpenAccess()
	if err := p.ensureOpenAccessAdministrator(ctx); err != nil {
		return err
	}

	check := p.config.Network.PublicIPCheck
	prober := egress.New(egress.Config{
		Enabled:  check.Enabled,
		Endpoint: check.Endpoint,
		Interval: check.Interval.AsDuration(),
		Client:   p.publicIPClient(),
		Clock:    clock.NewSystem(),
		Logger:   p.logger,
	})
	go prober.Run(ctx)

	server, err := apiserver.New(apiserver.Config{
		Bind:    p.config.Server.Bind,
		Port:    p.effectivePort,
		BaseURL: p.config.Server.BaseURL,
		Build: apiserver.BuildInfo{
			Version: p.build.Version, Milestone: p.build.Milestone, Commit: p.build.Commit,
			BuildDate: p.build.BuildDate, SourceURL: p.build.SourceURL, License: p.build.License,
			SQLiteDriver: store.DriverName(),
		},
		Checker:       health.New(p.settings, p.jobs, p.library),
		Auth:          authConfig,
		Jobs:          p.queue,
		Events:        p.bus,
		Clock:         clock.NewSystem(),
		Logger:        p.logger,
		WebUI:         webui.Handler(),
		Roots:         apiserver.FilesystemRoots{FS: p.fsys},
		DownloadRoots: apiserver.FilesystemRoots{FS: p.fsys},
		Scheduler: adapters.Schedule{
			Registry: p.taskRegistry, States: p.scheduleStates, Runners: p.taskRunners,
			Cache: p.cache, Clock: clock.NewSystem(),
		},
		CacheManager: adapters.CacheManager{
			Sweep: p.cacheSweep, Cache: p.cache, Registry: p.settingsRegistry, Clock: clock.NewSystem(),
		},
		LibrarySize: library.NewCounters(p.library),
		Works:       adapters.Works{Works: library.NewWorks(p.library), Clock: clock.NewSystem()},
		WorkDetail:  adapters.WorkDetail{Works: library.NewWorks(p.library)},
		WorksMonitor: adapters.WorksMonitor{
			Works: library.NewWorks(p.library), Desired: library.NewDesired(p.library),
			Categories: adapters.Categories{Cache: p.cache}, DefaultProfileID: p.defaultQualityProfileID,
			Logger: p.logger,
		},
		SystemHealth: adapters.SystemHealth{Cache: p.cache},
		Discover: adapters.Discover{
			Provider: p.musicBrainzProvider(), Identity: library.NewIdentity(p.library),
			Desired: library.NewDesired(p.library), People: library.NewPeople(p.library),
			Categories:       adapters.Categories{Cache: p.cache},
			DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(),
		},
		ArrSync: adapters.ArrSync{
			Cache: p.cache, Identity: library.NewIdentity(p.library), People: library.NewPeople(p.library),
			Subscriptions: library.NewSubscriptions(p.library), Desired: library.NewDesired(p.library),
			Imports: library.NewArrImports(p.library),
			Reverter: library.NewArrImportRevert(
				library.NewArrImports(p.library), library.NewIdentity(p.library),
				library.NewPeople(p.library), library.NewSubscriptions(p.library),
			),
			DefaultProfileID: p.defaultQualityProfileID, Clock: clock.NewSystem(), Lidarr: p.arrClientFactory(),
			Logger: p.logger,
		},
		IndexerTester: adapters.ConnectionTester{
			Cache: p.cache, Indexer: p.indexerClientFactory(), Downloader: p.downloaderClientFactory(),
			Clock: clock.NewSystem(),
		},
		DownloaderTester: adapters.ConnectionTester{
			Cache: p.cache, Indexer: p.indexerClientFactory(), Downloader: p.downloaderClientFactory(),
			Clock: clock.NewSystem(),
		},
		CandidateSearcher: p.candidateSearcher(),
		LibraryHealth: adapters.LibraryHealth{
			Physical: library.NewPhysical(p.library), Unidentified: library.NewUnidentified(p.library),
			Desired: library.NewDesired(p.library), BlockedContent: library.NewBlockedContentStore(p.library),
			Counters: library.NewCounters(p.library),
		},
		Search:       adapters.Search{Search: library.NewSearch(p.library)},
		Unidentified: adapters.Unidentified{Unidentified: library.NewUnidentified(p.library)},
		UnidentifiedResolver: adapters.UnidentifiedResolver{
			Unidentified: library.NewUnidentified(p.library), Attempts: library.NewAttempts(p.library),
			Identity: library.NewIdentity(p.library), Provider: p.musicBrainzProvider(), Clock: clock.NewSystem(),
		},
		Artwork:    adapters.Artwork{Artworks: library.NewArtworks(p.library)},
		FS:         p.fsys,
		ImagesDir:  filepath.Join(p.config.Paths.CacheDir, "images"),
		ConfigPath: p.configPath,
		People:     adapters.People{People: library.NewPeople(p.library)},
		PeopleSubscriber: adapters.People{Subscriptions: library.NewSubscriptions(p.library),
			People: library.NewPeople(p.library), Clock: clock.NewSystem(),
		},
		SubscriptionPreviewer: adapters.People{
			People: library.NewPeople(p.library), Provider: p.musicBrainzProvider(), Clock: clock.NewSystem(),
		},
		BlockedContent: adapters.BlockedContent{Blocks: library.NewBlockedContentStore(p.library)},
		Packs:          adapters.Packs{Packs: library.NewPacks(p.library)},
		RemoveAndBlocker: adapters.RemoveAndBlock{
			Action: library.NewRemoveAndBlock(
				library.NewQuarantine(p.library, p.fsys, clock.NewSystem()),
				library.NewBlockedContentStore(p.library), library.NewBlocklistStore(p.library),
				library.NewDesired(p.library), library.NewPhysical(p.library),
				adapters.RootFolderPath(p.cache),
			),
			Physical: library.NewPhysical(p.library), Clock: clock.NewSystem(),
		},
		ImportSessions: adapters.ImportSessions{
			Sessions: library.NewImportSessions(p.library),
			Revert: library.NewImportRevert(
				library.NewImportSessions(p.library), library.NewPhysical(p.library), library.NewIdentity(p.library),
				library.NewQuarantine(p.library, p.fsys, clock.NewSystem()), adapters.RootFolderPath(p.cache),
			),
			Manual: p.manualCommitter(),
			Clock:  clock.NewSystem(),
		},
		ImportInspector: adapters.ImportInspector{Queue: p.queue},
		DrainStarter: adapters.Drain{
			Config: drainjob.Config{
				Roots: adapters.DrainRoots{Cache: p.cache}, Files: library.NewPhysical(p.library),
				MoveJobs: library.NewMoveJobs(p.library), Queue: p.queue, Clock: clock.NewSystem(),
			},
			Cache: p.cache,
		},
		MoveJobs:       adapters.MoveJobs{MoveJobs: library.NewMoveJobs(p.library)},
		Navigation:     adapters.Navigation{},
		LibraryTracks:  adapters.Tracks{Tracks: library.NewTrackParts(p.library)},
		LibraryArtists: adapters.Artists{People: library.NewPeople(p.library)},
		RenameStarter: adapters.Rename{Config: music.RenameConfig{
			Roots: adapters.DrainRoots{Cache: p.cache}, Files: library.NewPhysical(p.library),
			Identities: library.NewDesired(p.library), Works: library.NewWorks(p.library),
			MoveJobs: library.NewMoveJobs(p.library), Queue: p.queue, Clock: clock.NewSystem(),
		}},
		// time.Local.String() reports "Local"; the name the operator set is the useful one.
		Timezone: p.timezone,
		Egress:   adapters.Egress{Prober: prober},
	})
	if err != nil {
		return err
	}
	p.server = server

	listener, err := server.Listen()
	if err != nil {
		return err
	}

	stopping, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := p.startBackupSchedules(stopping); err != nil {
		return err
	}

	dispatcherDone := make(chan struct{})
	go func() {
		defer close(dispatcherDone)
		p.dispatcher.Run(stopping)
	}()

	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()

	server.MarkReady()
	p.logger.Info("serving",
		slog.String("address", server.Address()),
		slog.String("port_source", portSource),
		slog.String("version", p.build.Version),
		slog.String("sqlite_driver", store.DriverName()),
		slog.String("mode", string(p.config.Mode)))

	select {
	case err := <-serverDone:
		return err
	case <-stopping.Done():
	}

	// Shutdown order matters (section 15.2): stop accepting first, then let the
	// dispatcher drain, then close the databases in p.close.
	p.logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		p.logger.Warn("server shutdown", slog.Any("error", err))
	}
	<-dispatcherDone
	return nil
}

// close drains and closes the databases. The write queues drain before their
// handles close, which is bounded because every transaction is bounded.
func (p *process) close() {
	for _, opened := range []*store.Store{p.library, p.jobs, p.settings, p.metadata} {
		if opened == nil {
			continue
		}
		if err := opened.Close(); err != nil {
			p.logger.Warn("closing database", slog.Any("error", err))
		}
	}
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return "unknown-host"
	}
	return name
}

// authConfig turns the validated configuration into the server's authentication wiring.
func (p *process) authConfig() (apiserver.AuthConfig, error) {
	key, err := config.DecodeKey(p.config.Security.EncryptionKey)
	if err != nil {
		return apiserver.AuthConfig{}, err
	}

	networks := make([]netip.Prefix, 0, len(p.config.Security.TrustedProxyCIDRs))
	for _, raw := range p.config.Security.TrustedProxyCIDRs {
		network, err := netip.ParsePrefix(raw)
		if err != nil {
			return apiserver.AuthConfig{}, fmt.Errorf("security.trusted_proxy_cidrs: %q: %w", raw, err)
		}
		networks = append(networks, network)
	}

	wall := clock.NewSystem()
	return apiserver.AuthConfig{
		Mode:          apiserver.AuthMode(p.config.Security.AuthMode),
		Sessions:      auth.NewSessions(key, wall, auth.DefaultSessionTTL),
		Settings:      p.cache,
		Registry:      p.settingsRegistry,
		Clock:         wall,
		ProxyHeader:   p.config.Security.TrustedProxyHeader,
		ProxyNetworks: networks,
	}, nil
}

// reportRootFolders says, once at startup, whether the library can actually be written -- and
// then carries on regardless.
func (p *process) reportRootFolders() {
	roots := apiserver.FilesystemRoots{FS: p.fsys}

	for _, folder := range p.cache.Snapshot().RootFolders() {
		if err := roots.Validate(folder.Path); err != nil {
			p.logger.Warn("library root is unusable", "path", folder.Path, "detail", err.Error())
			continue
		}
		if err := roots.Writable(folder.Path); err != nil {
			p.logger.Warn("library root is not writable; imports into it will fail",
				"path", folder.Path, "detail", err.Error())
		}
	}
}
