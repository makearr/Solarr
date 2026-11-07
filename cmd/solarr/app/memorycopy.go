package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/ramcopy"
)

// memoryWatchInterval is how often the container's free memory is looked at while a RAM copy runs.
const memoryWatchInterval = 30 * time.Second

// memoryFloor is the free memory below which the RAM copies are given up: a twentieth of the limit, at least this.
const memoryFloor = 256 << 20

// shmHeadroom is how much larger than what it will hold the RAM directory must be: the files plus a fifth.
const shmHeadroom = 1.2

// noticeFile records, beside config.yml, why a RAM copy was switched off, until the next save of the setting.
const noticeFile = "memory-copies.notice"

// memoryCopy is the cache and the databases running from RAM, and the copying back to disk.
type memoryCopy struct {
	p  *process
	mu sync.Mutex

	cacheRunning, databaseRunning bool
	// diskDBDir and diskCacheDir are where the data really lives; the process works in ramDBDir and ramCacheDir.
	diskDBDir, diskCacheDir string
	ramDBDir, ramCacheDir   string
	lastCacheSync           time.Time
	lastDatabaseSync        time.Time
	stamps                  map[string]string
	notice                  string
	// configured is what config.yml holds now, which differs from what runs until the next start.
	configured config.Memory
}

func (p *process) imagesDir() string {
	if p.memory != nil && p.memory.cacheRunning {
		return filepath.Join(p.memory.ramCacheDir, "images")
	}
	return filepath.Join(p.config.Paths.CacheDir, "images")
}

// metadataDir is where metadata.db lives: in the cache, or in its RAM copy.
func (p *process) metadataDir() string {
	if p.memory != nil && p.memory.cacheRunning {
		return p.memory.ramCacheDir
	}
	return p.config.Paths.CacheDir
}

// stageMemoryCopies puts the databases and the cache into RAM when config.yml asks and the container has the memory;
// otherwise it says why not and runs from disk. It runs before any database opens.
func (p *process) stageMemoryCopies(ctx context.Context) error {
	p.memory = &memoryCopy{p: p, stamps: map[string]string{}, configured: p.config.Memory}
	settings := p.config.Memory
	p.memory.notice = p.readMemoryNotice()
	if !settings.CacheTmpfs && !settings.DatabaseTmpfs {
		return nil
	}
	memory, err := ramcopy.ReadMemory(p.fsys)
	if err != nil {
		p.logger.Warn("RAM copies not started: the container's memory could not be read", slog.Any("error", err))
		return nil
	}
	cacheDisk, databaseDisk, err := p.memory.onDisk(ctx)
	if err != nil {
		return err
	}
	wantCache, wantDatabase := settings.CacheTmpfs, settings.DatabaseTmpfs
	total, free := p.tmpfsSize(settings.TmpfsDir)
	p.logger.Info("RAM directory", slog.String("dir", settings.TmpfsDir), slog.Int64("size_bytes", total), slog.Int64("free_bytes", free),
		slog.Int64("cache_on_disk_bytes", cacheDisk), slog.Int64("databases_on_disk_bytes", databaseDisk))
	fits := func(cache, database bool) bool {
		var onDisk int64
		if cache {
			onDisk += cacheDisk
		}
		if database {
			onDisk += databaseDisk
		}
		return shmLargeEnough(total, onDisk) && ramcopy.WithHeadroom(onDisk) <= memory.AvailableBytes && ramcopy.WithHeadroom(onDisk) <= free
	}
	// The cache goes first when both do not fit, as it is the one that can be fetched again.
	if !fits(wantCache, wantDatabase) && wantCache {
		wantCache = false
		p.memory.notice = "The cache was not kept in RAM at start: it did not fit in the memory the container has free."
	}
	if !fits(wantCache, wantDatabase) && wantDatabase {
		wantDatabase = false
		p.memory.notice = "The databases were not kept in RAM at start: they did not fit in the memory the container has free."
	}
	if err := p.memory.start(ctx, wantCache, wantDatabase); err != nil {
		return err
	}
	if p.memory.notice != "" {
		p.logger.Warn(p.memory.notice, slog.Int64("available_bytes", memory.AvailableBytes), slog.Int64("tmpfs_free_bytes", free))
	}
	return nil
}

// onDisk is how much the cache's pictures and responses, and the four databases, are on disk now.
func (m *memoryCopy) onDisk(ctx context.Context) (cache, database int64, err error) {
	paths := m.p.config.Paths
	images, err := ramcopy.DirBytes(ctx, m.p.fsys, filepath.Join(paths.CacheDir, "images"))
	if err != nil {
		return 0, 0, err
	}
	metadata := fileBytes(m.p.fsys, filepath.Join(paths.CacheDir, "metadata.db"))
	var databases int64
	for _, name := range []string{"library", "settings", "jobs", "statistics"} {
		databases += fileBytes(m.p.fsys, filepath.Join(paths.DBDir, name+".db"))
	}
	return images + metadata, databases, nil
}

func fileBytes(fsys fsx.FS, path string) int64 {
	info, err := fsys.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size
}

// shmLargeEnough says the RAM directory is at least a fifth larger than what it would hold.
func shmLargeEnough(total, onDisk int64) bool { return float64(total) >= float64(onDisk)*shmHeadroom }

// tmpfsSize is the size and the free room of the filesystem the RAM directory is on.
func (p *process) tmpfsSize(dir string) (total, free int64) {
	for candidate := dir; candidate != "/" && candidate != "."; candidate = filepath.Dir(candidate) {
		if size, room, err := p.fsys.DiskUsage(candidate); err == nil {
			return size, room
		}
	}
	return 0, 0
}

// start copies the chosen folders into RAM and points the process at the copies.
func (m *memoryCopy) start(ctx context.Context, cache, database bool) error {
	p := m.p
	root := p.config.Memory.TmpfsDir
	if err := p.fsys.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("memory copies: create %s: %w", root, err)
	}
	if database {
		m.diskDBDir, m.ramDBDir = p.config.Paths.DBDir, filepath.Join(root, "db")
		if err := m.load(ctx, m.diskDBDir, m.ramDBDir, func(name string) bool { return strings.HasSuffix(name, ".db") }); err != nil {
			return err
		}
		p.config.Paths.DBDir = m.ramDBDir
		m.databaseRunning = true
	}
	if cache {
		m.diskCacheDir, m.ramCacheDir = p.config.Paths.CacheDir, filepath.Join(root, "cache")
		onlyCache := func(name string) bool { return name == "metadata.db" }
		if err := m.load(ctx, m.diskCacheDir, m.ramCacheDir, onlyCache); err != nil {
			return err
		}
		if _, _, err := ramcopy.Sync(ctx, p.fsys, filepath.Join(m.diskCacheDir, "images"), filepath.Join(m.ramCacheDir, "images")); err != nil {
			return fmt.Errorf("memory copies: load the pictures: %w", err)
		}
		m.cacheRunning = true
	}
	return nil
}

// load replaces a RAM folder with the chosen files of a disk folder.
func (m *memoryCopy) load(_ context.Context, disk, ram string, take func(name string) bool) error {
	fsys := m.p.fsys
	if err := fsys.MkdirAll(ram, 0o700); err != nil {
		return err
	}
	entries, err := fsys.ReadDir(disk)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir || !take(entry.Name) {
			continue
		}
		if err := ramcopy.CopyFile(fsys, filepath.Join(disk, entry.Name), filepath.Join(ram, entry.Name)); err != nil {
			return fmt.Errorf("memory copies: copy %s into RAM: %w", entry.Name, err)
		}
	}
	return nil
}

// startSync runs the copying back to disk and the memory watch until ctx ends.
func (m *memoryCopy) startSync(ctx context.Context) {
	wall := clock.NewSystem()
	settings := m.p.config.Memory
	if m.databaseRunning {
		go m.every(ctx, wall, settings.DatabaseSyncInterval.AsDuration(), m.syncDatabases)
	}
	if m.cacheRunning {
		go m.every(ctx, wall, settings.CacheSyncInterval.AsDuration(), m.syncCache)
	}
	if m.databaseRunning || m.cacheRunning {
		go m.every(ctx, wall, memoryWatchInterval, m.watch)
	}
}

func (m *memoryCopy) every(ctx context.Context, wall clock.Clock, interval time.Duration, work func(context.Context) error) {
	ticker := wall.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			if err := work(ctx); err != nil && ctx.Err() == nil {
				m.p.logger.Error("copying from RAM to disk failed", slog.Any("error", err))
			}
		}
	}
}

func (m *memoryCopy) databaseStores() []*store.Store {
	return []*store.Store{m.p.settings, m.p.jobs, m.p.library, m.p.statistics}
}

// syncDatabases writes a snapshot of each changed database over its disk file. A snapshot holds that database's writer.
func (m *memoryCopy) syncDatabases(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var failures []error
	for _, target := range m.databaseStores() {
		if err := m.snapshot(ctx, target, m.ramDBDir, m.diskDBDir); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		m.lastDatabaseSync = clock.NewSystem().Now()
	}
	return errors.Join(failures...)
}

func (m *memoryCopy) snapshot(ctx context.Context, target *store.Store, ramDir, diskDir string) error {
	name := target.Name() + ".db"
	stamp := m.fileStamp(filepath.Join(ramDir, name))
	if m.stamps[name] == stamp && stamp != "" {
		return nil
	}
	fsys := m.p.fsys
	partial := filepath.Join(diskDir, name+".syncing")
	_ = fsys.Remove(partial)
	if err := target.SnapshotTo(ctx, partial); err != nil {
		return err
	}
	if err := fsys.Rename(partial, filepath.Join(diskDir, name)); err != nil {
		return fmt.Errorf("replace %s on disk: %w", name, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = fsys.Remove(filepath.Join(diskDir, name+suffix))
	}
	m.stamps[name] = stamp
	return nil
}

// fileStamp changes whenever the database or its log is written, so an idle database is not copied again.
func (m *memoryCopy) fileStamp(path string) string {
	var stamp string
	for _, suffix := range []string{"", "-wal"} {
		if info, err := m.p.fsys.Stat(path + suffix); err == nil {
			stamp += fmt.Sprintf("%d@%d;", info.Size, info.ModTime.UnixNano())
		}
	}
	return stamp
}

// syncCache mirrors the RAM pictures to disk and snapshots metadata.db.
func (m *memoryCopy) syncCache(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, _, err := ramcopy.Sync(ctx, m.p.fsys, filepath.Join(m.ramCacheDir, "images"), filepath.Join(m.diskCacheDir, "images")); err != nil {
		return err
	}
	if m.p.metadata != nil {
		if err := m.snapshot(ctx, m.p.metadata, m.ramCacheDir, m.diskCacheDir); err != nil {
			return err
		}
	}
	m.lastCacheSync = clock.NewSystem().Now()
	return nil
}

// finalSync is the last copy at shutdown, before the databases close.
func (m *memoryCopy) finalSync(ctx context.Context) {
	if m == nil {
		return
	}
	if m.databaseRunning {
		if err := m.syncDatabases(ctx); err != nil {
			m.p.logger.Error("the final copy of the databases to disk failed", slog.Any("error", err))
		}
	}
	if m.cacheRunning {
		if err := m.syncCache(ctx); err != nil {
			m.p.logger.Error("the final copy of the cache to disk failed", slog.Any("error", err))
		}
	}
}

// watch gives up the RAM copies when the container runs short of memory: the cache first, the databases only when
// that does not free enough. Each is copied to disk and switched off in config.yml, and Solarr then stops so that it
// starts again from disk.
func (m *memoryCopy) watch(ctx context.Context) error {
	memory, err := ramcopy.ReadMemory(m.p.fsys)
	if err != nil {
		return err
	}
	floor := max(int64(memoryFloor), memory.LimitBytes/20)
	if memory.AvailableBytes >= floor {
		return nil
	}
	settings := m.configured
	reason := fmt.Sprintf("The container had %d MB of memory left (limit %d MB).", memory.AvailableBytes>>20, memory.LimitBytes>>20)
	if m.cacheRunning {
		settings.CacheTmpfs = false
		freed, _ := ramcopy.DirBytes(ctx, m.p.fsys, m.ramCacheDir)
		if !m.databaseRunning || memory.AvailableBytes+freed >= floor {
			reason += " The cache was moved back to disk."
		} else {
			settings.DatabaseTmpfs = false
			reason += " The cache and the databases were moved back to disk."
		}
	} else {
		settings.DatabaseTmpfs = false
		reason += " The databases were moved back to disk."
	}
	m.p.logger.Error("out of memory: leaving RAM copies", slog.String("reason", reason))
	m.finalSync(ctx)
	if err := m.p.saveMemorySettings(settings); err != nil {
		return err
	}
	m.p.writeMemoryNotice(reason + " Solarr restarted to apply it.")
	m.p.requestStop()
	return nil
}

func (p *process) noticePath() string { return filepath.Join(p.config.Paths.ConfigDir, noticeFile) }

func (p *process) readMemoryNotice() string {
	file, err := p.fsys.Open(p.noticePath())
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(io.LimitReader(file, 4096))
	return strings.TrimSpace(string(data))
}

func (p *process) writeMemoryNotice(text string) {
	_ = fsx.WriteAtomic(p.fsys, p.noticePath(), func(w io.Writer) error { _, err := io.WriteString(w, text+"\n"); return err })
}

// saveMemorySettings rewrites the memory section of config.yml; the change applies at the next start.
func (p *process) saveMemorySettings(settings config.Memory) error {
	file, err := p.fsys.Open(p.configPath)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(file, config.MaxDocumentBytes))
	_ = file.Close()
	if err != nil {
		return err
	}
	patched, err := config.SetMemory(data, settings)
	if err != nil {
		return err
	}
	return fsx.WriteAtomicSecret(p.fsys, p.configPath, func(w io.Writer) error { _, err := w.Write(patched); return err })
}

// memoryCopies serves the Cache page's RAM options.
type memoryCopies struct{ p *process }

func (a memoryCopies) Status(ctx context.Context) (apiserver.MemoryCopyStatus, error) {
	p := a.p
	p.memory.mu.Lock()
	settings := p.memory.configured
	p.memory.mu.Unlock()
	cacheDisk, databaseDisk, err := p.memory.onDisk(ctx)
	if err != nil {
		return apiserver.MemoryCopyStatus{}, err
	}
	cache, database := ramcopy.WithHeadroom(cacheDisk), ramcopy.WithHeadroom(databaseDisk)
	memory, memoryErr := ramcopy.ReadMemory(p.fsys)
	shmTotal, shmFree := p.tmpfsSize(settings.TmpfsDir)
	p.memory.mu.Lock()
	defer p.memory.mu.Unlock()
	status := apiserver.MemoryCopyStatus{
		CacheEnabled: settings.CacheTmpfs, DatabaseEnabled: settings.DatabaseTmpfs,
		CacheRunning: p.memory.cacheRunning, DatabaseRunning: p.memory.databaseRunning,
		CacheSyncMinutes: int64(settings.CacheSyncInterval.AsDuration() / time.Minute), DatabaseSyncMinutes: int64(settings.DatabaseSyncInterval.AsDuration() / time.Minute),
		CacheBytesNeeded: cache, DatabaseBytesNeeded: database,
		TmpfsFreeBytes: shmFree, TmpfsTotalBytes: shmTotal, TmpfsDir: settings.TmpfsDir, Notice: p.memory.notice,
		CacheDiskBytes: cacheDisk, DatabaseDiskBytes: databaseDisk,
	}
	if memoryErr == nil {
		status.AvailableBytes, status.LimitBytes, status.MemorySource = memory.AvailableBytes, memory.LimitBytes, memory.Source
	}
	if !p.memory.lastCacheSync.IsZero() {
		at := p.memory.lastCacheSync
		status.LastCacheSync = &at
	}
	if !p.memory.lastDatabaseSync.IsZero() {
		at := p.memory.lastDatabaseSync
		status.LastDatabaseSync = &at
	}
	return status, nil
}

func (a memoryCopies) Set(ctx context.Context, change apiserver.MemoryCopySettings) error {
	p := a.p
	if change.CacheEnabled || change.DatabaseEnabled {
		if !change.Acknowledged {
			return apiserver.ErrRiskNotAcknowledged
		}
		memory, err := ramcopy.ReadMemory(p.fsys)
		if err != nil {
			return err
		}
		cacheDisk, databaseDisk, err := p.memory.onDisk(ctx)
		if err != nil {
			return err
		}
		var onDisk int64
		if change.CacheEnabled {
			onDisk += cacheDisk
		}
		if change.DatabaseEnabled {
			onDisk += databaseDisk
		}
		need := ramcopy.WithHeadroom(onDisk)
		total, free := p.tmpfsSize(p.config.Memory.TmpfsDir)
		switch {
		case !shmLargeEnough(total, onDisk):
			return fmt.Errorf("%w: %s is %d MB and must be at least 20%% larger than the %d MB it would hold; raise shm_size",
				apiserver.ErrNotEnoughMemory, p.config.Memory.TmpfsDir, total>>20, onDisk>>20)
		case need > memory.AvailableBytes:
			return fmt.Errorf("%w: it needs %d MB and the container has %d MB free", apiserver.ErrNotEnoughMemory, need>>20, memory.AvailableBytes>>20)
		case need > free:
			return fmt.Errorf("%w: %s has %d MB free, %d MB are needed", apiserver.ErrNotEnoughMemory, p.config.Memory.TmpfsDir, free>>20, need>>20)
		}
	}
	p.memory.mu.Lock()
	settings := p.memory.configured
	p.memory.mu.Unlock()
	settings.CacheTmpfs, settings.DatabaseTmpfs = change.CacheEnabled, change.DatabaseEnabled
	if change.CacheSyncMinutes > 0 {
		settings.CacheSyncInterval = config.Duration(time.Duration(change.CacheSyncMinutes) * time.Minute)
	}
	if change.DatabaseSyncMinutes > 0 {
		settings.DatabaseSyncInterval = config.Duration(time.Duration(change.DatabaseSyncMinutes) * time.Minute)
	}
	if err := p.saveMemorySettings(settings); err != nil {
		return err
	}
	_ = p.fsys.Remove(p.noticePath())
	p.memory.mu.Lock()
	p.memory.configured = settings
	p.memory.notice = ""
	p.memory.mu.Unlock()
	return nil
}
