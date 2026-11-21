package app

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/moving/moverjob"
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

// Work that changed files asks for a copy of the databases; requests arriving together make one copy after the debounce (RAM-1).
func TestSyncAfterRequests_TwoRequestsMakeOneCopyAfterTheDebounce(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	requests := make(chan struct{}, 1)
	copied := make(chan struct{}, 4)
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		syncAfterRequests(ctx, fake, 30*time.Second, requests, func(context.Context) error { copied <- struct{}{}; return nil }, slog.New(slog.DiscardHandler))
	}()
	requests <- struct{}{}
	fake.BlockUntilWaiters(1)
	requests <- struct{}{}
	fake.Advance(29 * time.Second)
	if len(copied) != 0 {
		t.Fatal("copied before the debounce ended")
	}
	fake.Advance(time.Second)
	select {
	case <-copied:
	case <-time.After(3 * time.Second):
		t.Fatal("no copy after the debounce")
	}
	fake.Advance(time.Minute)
	time.Sleep(50 * time.Millisecond)
	stop()
	<-done
	if len(copied) != 0 {
		t.Errorf("%d further copies, want the two requests to make one", len(copied))
	}
}

type noRunLog struct{}

func (noRunLog) RecordRun(string, time.Time, time.Time) {}

// Only jobs that place, move or recycle files ask for a database copy, and only while the databases run in RAM (RAM-1).
func TestDiskWorkRunLog_AskForACopyOnlyAfterJobsThatChangeFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		jobType   string
		inRAM     bool
		wantAsked bool
	}{
		{name: "an import", jobType: string(importjob.JobType), inRAM: true, wantAsked: true},
		{name: "a move", jobType: string(moverjob.JobType), inRAM: true, wantAsked: true},
		{name: "a job that touches no file", jobType: "scan.probe", inRAM: true},
		{name: "an import with the databases on disk", jobType: string(importjob.JobType)},
	} {
		t.Run(test.name, func(t *testing.T) {
			memory := &memoryCopy{databaseRunning: test.inRAM, syncRequested: make(chan struct{}, 1)}
			diskWorkRunLog{RunLog: noRunLog{}, memory: memory}.RecordRun(test.jobType, time.Time{}, time.Time{})
			if asked := len(memory.syncRequested) == 1; asked != test.wantAsked {
				t.Errorf("copy asked = %v, want %v", asked, test.wantAsked)
			}
		})
	}
}
