package main

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
	"syscall"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/egress"
	"github.com/makearr/MediaNexus/internal/events"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/health"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/privilege"
	"github.com/makearr/MediaNexus/internal/recovery"
	"github.com/makearr/MediaNexus/internal/settings"
	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/webui"
	"github.com/makearr/MediaNexus/migrations"
)

// process is everything the server owns, so shutdown has one place to look.
type process struct {
	config     config.Config
	fsys       fsx.FS
	logger     *slog.Logger
	settings   *store.Store
	jobs       *store.Store
	library    *store.Store
	metadata   *store.Store
	queue      *jobs.Queue
	cache      *settings.Cache
	bus        *events.Bus
	dispatcher *jobs.Dispatcher
	server     *apiserver.Server
	timezone   string
	// defaultQualityProfileID is ensureDefaultQualityProfile's fallback,
	// used when a category has none of its own configured (todo 4.4).
	defaultQualityProfileID int64
}

// serve is the startup sequence of architecture section 15.1, in order.
// Refusing to start is preferred over starting with silent data loss.
func serve(ctx context.Context, configPath string, out io.Writer) error {
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

	running := &process{config: loaded, fsys: fsys, logger: logger, timezone: timezone}
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
	if err := p.registerProbeJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerCapacityJob(registry, executor); err != nil {
		return err
	}
	if err := p.registerSubscriptionExpandJob(registry, executor); err != nil {
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

func (p *process) startServer(ctx context.Context) error {
	authConfig, err := p.authConfig()
	if err != nil {
		return err
	}

	p.reportRootFolders()
	p.enqueueLibraryScans(ctx)
	p.enqueueEnrichment(ctx)
	p.enqueueSubscriptionExpand(ctx)
	p.enqueueArtwork(ctx)
	p.enqueueCategoryCounts(ctx)
	p.enqueueProbe(ctx)
	p.enqueueCapacity(ctx)
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
		Port:    p.config.Server.Port,
		BaseURL: p.config.Server.BaseURL,
		Build: apiserver.BuildInfo{
			Version: version, Commit: commit, BuildDate: buildDate,
			SourceURL: sourceURL, License: license,
			SQLiteDriver: store.DriverName(),
		},
		Checker:     health.New(p.settings, p.jobs, p.library),
		Auth:        authConfig,
		Jobs:        p.queue,
		Events:      p.bus,
		Clock:       clock.NewSystem(),
		Logger:      p.logger,
		WebUI:       webui.Handler(),
		Roots:       apiserver.FilesystemRoots{FS: p.fsys},
		LibrarySize: library.NewCounters(p.library),
		Works:       worksAdapter{works: library.NewWorks(p.library)},
		WorkDetail:  workDetailAdapter{works: library.NewWorks(p.library)},
		WorksMonitor: worksMonitorAdapter{
			works: library.NewWorks(p.library), desired: library.NewDesired(p.library),
			categories: cacheCategories{cache: p.cache}, defaultProfileID: p.defaultQualityProfileID,
			logger: p.logger,
		},
		Search:       searchAdapter{search: library.NewSearch(p.library)},
		Unidentified: unidentifiedAdapter{unidentified: library.NewUnidentified(p.library)},
		UnidentifiedResolver: unidentifiedResolverAdapter{
			unidentified: library.NewUnidentified(p.library), identity: library.NewIdentity(p.library),
			provider: p.musicBrainzProvider(), clock: clock.NewSystem(),
		},
		Artwork:          artworkAdapter{artworks: library.NewArtworks(p.library)},
		FS:               p.fsys,
		ImagesDir:        filepath.Join(p.config.Paths.CacheDir, "images"),
		People:           peopleAdapter{people: library.NewPeople(p.library)},
		PeopleSubscriber: peopleAdapter{people: library.NewPeople(p.library)},
		// time.Local.String() reports "Local"; the name the operator set is the useful one.
		Timezone: p.timezone,
		Egress:   egressStatus{prober: prober},
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
		slog.String("version", version),
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
