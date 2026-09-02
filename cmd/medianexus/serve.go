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
	"syscall"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/auth"
	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/events"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/health"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/privilege"
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
	queue      *jobs.Queue
	cache      *settings.Cache
	bus        *events.Bus
	dispatcher *jobs.Dispatcher
	server     *apiserver.Server
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

	running := &process{config: loaded, fsys: fsys, logger: logger}
	defer running.close()

	if err := running.openDatabases(ctx); err != nil {
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

	settingsStore, err := store.Open(ctx, wall, p.storeConfig("settings"))
	if err != nil {
		return err
	}
	p.settings = settingsStore

	jobsStore, err := store.Open(ctx, wall, p.storeConfig("jobs"))
	if err != nil {
		return err
	}
	p.jobs = jobsStore

	libraryStore, err := store.Open(ctx, wall, p.storeConfig("library"))
	if err != nil {
		return err
	}
	p.library = libraryStore

	// Migrations back up first and refuse to run without somewhere to back up
	// to (Invariant 7).
	backupDir := p.config.Paths.BackupDir
	settingsOwner, err := migrations.Settings()
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, settingsStore, backupDir, settingsOwner); err != nil {
		return err
	}
	jobsOwner, err := migrations.Jobs()
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, jobsStore, backupDir, jobsOwner); err != nil {
		return err
	}
	libraryOwner, err := migrations.Library()
	if err != nil {
		return err
	}
	if err := store.Migrate(ctx, libraryStore, backupDir, libraryOwner); err != nil {
		return err
	}

	cache, err := settings.NewCache(ctx, settingsStore)
	if err != nil {
		return err
	}
	p.cache = cache
	return nil
}

func (p *process) storeConfig(name string) store.Config {
	return store.Config{
		Name:        name,
		Path:        fmt.Sprintf("%s/%s.db", p.config.Paths.DBDir, name),
		BusyTimeout: p.config.Database.BusyTimeout.AsDuration(),
		PageSize:    p.config.Database.PageSize,
		CacheSizeKB: p.config.Database.CacheSizeKB,
		WriteWarn:   p.config.Database.WriteWarn.AsDuration(),
		WriteHard:   p.config.Database.WriteHard.AsDuration(),
		Logger:      p.logger,
	}
}

func (p *process) startJobs(_ context.Context) error {
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

	server, err := apiserver.New(apiserver.Config{
		Bind:    p.config.Server.Bind,
		Port:    p.config.Server.Port,
		BaseURL: p.config.Server.BaseURL,
		Build: apiserver.BuildInfo{
			Version: version, Commit: commit, BuildDate: buildDate,
			SourceURL: sourceURL, License: license,
		},
		Checker: health.New(p.settings, p.jobs, p.library),
		Auth:    authConfig,
		Jobs:    p.queue,
		Events:  p.bus,
		Clock:   clock.NewSystem(),
		Logger:  p.logger,
		WebUI:   webui.Handler(),
		Roots:   apiserver.FilesystemRoots{FS: p.fsys},
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
	for _, opened := range []*store.Store{p.library, p.jobs, p.settings} {
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
