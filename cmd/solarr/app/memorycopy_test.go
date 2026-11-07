package app

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func putFile(t *testing.T, fsys fsx.FS, path, content string) {
	t.Helper()
	if err := fsys.MkdirAll(path[:strings.LastIndex(path, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(file, content)
	_ = file.Close()
}

// Out of memory with only the cache in RAM: the cache is copied to disk, switched off in config.yml, and Solarr stops.
func TestMemoryWatch_LeavesTheCacheFirstWhenTheContainerRunsOutOfMemory(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	putFile(t, fsys, "/sys/fs/cgroup/memory.max", "1073741824\n")
	putFile(t, fsys, "/sys/fs/cgroup/memory.current", "1063256064\n")
	putFile(t, fsys, "/config/config.yml", "version: 1\nmemory:\n  cache_tmpfs: true\n  database_tmpfs: false\n  cache_sync_interval: 10m\n  database_sync_interval: 5m\n  tmpfs_dir: /dev/shm/solarr\n")
	putFile(t, fsys, "/dev/shm/solarr/cache/images/a.webp", "picture")

	settings := config.Default().Memory
	settings.CacheTmpfs = true
	stopped := false
	running := &process{fsys: fsys, logger: slog.New(slog.DiscardHandler), configPath: "/config/config.yml", requestStop: func() { stopped = true }}
	running.config.Paths.ConfigDir = "/config"
	running.memory = &memoryCopy{
		p: running, cacheRunning: true, configured: settings, stamps: map[string]string{},
		ramCacheDir: "/dev/shm/solarr/cache", diskCacheDir: "/disk/cache",
	}

	if err := running.memory.watch(t.Context()); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if !stopped {
		t.Error("Solarr was not asked to stop")
	}
	if _, err := fsys.Stat("/disk/cache/images/a.webp"); err != nil {
		t.Errorf("the cache was not copied to disk first: %v", err)
	}
	file, _ := fsys.Open("/config/config.yml")
	data, _ := io.ReadAll(file)
	if !strings.Contains(string(data), "cache_tmpfs: false") {
		t.Errorf("config.yml = %q, want the cache switched off", data)
	}
	if !strings.Contains(running.readMemoryNotice(), "cache was moved back to disk") {
		t.Errorf("notice = %q", running.readMemoryNotice())
	}
}

func TestMemoryWatch_DoesNothingWhileThereIsRoom(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	putFile(t, fsys, "/sys/fs/cgroup/memory.max", "8589934592\n")
	putFile(t, fsys, "/sys/fs/cgroup/memory.current", "1073741824\n")
	stopped := false
	running := &process{fsys: fsys, logger: slog.New(slog.DiscardHandler), requestStop: func() { stopped = true }}
	running.memory = &memoryCopy{p: running, cacheRunning: true, stamps: map[string]string{}}
	if err := running.memory.watch(t.Context()); err != nil || stopped {
		t.Errorf("watch = %v, stopped = %v; want neither", err, stopped)
	}
}

func TestShmLargeEnough_NeedsAFifthMoreThanWhatItWillHold(t *testing.T) {
	for _, test := range []struct {
		total, onDisk int64
		want          bool
	}{{1200, 1000, true}, {1199, 1000, false}, {64 << 20, 100 << 20, false}, {8 << 30, 2 << 30, true}, {0, 0, true}} {
		if got := shmLargeEnough(test.total, test.onDisk); got != test.want {
			t.Errorf("shmLargeEnough(%d, %d) = %v, want %v", test.total, test.onDisk, got, test.want)
		}
	}
}
