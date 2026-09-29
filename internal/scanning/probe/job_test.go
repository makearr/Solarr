package probe_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/probe"
	"github.com/makearr/Solarr/migrations"
)

func newLibrary(t *testing.T) *store.Store {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return opened
}

type fakeRoots map[int64]settings.RootFolder

func (f fakeRoots) RootFolder(id int64) (settings.RootFolder, bool) {
	root, found := f[id]
	return root, found
}

// fakeTool answers every path with the same fixed measurement.
type fakeTool struct{ measured mediatool.Measured }

func (f *fakeTool) Available(context.Context) error { return nil }

func (f *fakeTool) ReadTags(_ context.Context, paths []string) ([]mediatool.Probe, error) {
	probes := make([]mediatool.Probe, len(paths))
	for index, p := range paths {
		probes[index] = mediatool.Probe{Path: p, Measured: f.measured}
	}
	return probes, nil
}

func (f *fakeTool) WriteTags(context.Context, []mediatool.TagEdit) ([]error, error) {
	return nil, nil
}

func (f *fakeTool) Fingerprint(context.Context, []string) ([]mediatool.Fingerprint, error) {
	return nil, nil
}

func TestJob_RunProbesEveryUnmeasuredFile(t *testing.T) {
	opened := newLibrary(t)
	physical := library.NewPhysical(opened)

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "track.flac"), []byte("not real audio, the tool is faked"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	ids, err := physical.RegisterBatch(t.Context(), 1, []library.PhysicalFile{
		{RelPath: "track.flac", SizeBytes: 34, QuickHash: "hash-1", FirstSeenAt: time.Unix(0, 0)},
	})
	if err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}

	job := probe.NewJob(probe.Config{
		Physical: physical,
		Coverage: library.NewProbeCoverage(opened, clock.NewFakeAtEpoch()),
		Roots:    fakeRoots{1: settings.RootFolder{ID: 1, Path: root, Online: true}},
		Tool: &fakeTool{measured: mediatool.Measured{
			FormatName: "flac", AudioCodec: "flac", SampleRate: 44100,
			BitDepth: 16, Channels: 2, AudioBitrate: 705600, OverallBitrate: 705600,
			Duration: 3 * time.Second,
		}},
		Clock:  clock.NewFakeAtEpoch(),
		Logger: slog.New(slog.DiscardHandler),
	})

	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	object, err := librarydb.New(opened.Read()).GetMediaObject(t.Context(), ids[0].MediaObjectID)
	if err != nil {
		t.Fatalf("GetMediaObject: %v", err)
	}
	if !object.MeasuredAttributesID.Valid {
		t.Fatal("MeasuredAttributesID was not set after Run")
	}
	if object.OverallBitrate.Int64 != 705600 {
		t.Errorf("OverallBitrate = %d, want 705600", object.OverallBitrate.Int64)
	}

	attrs, err := librarydb.New(opened.Read()).GetAttributes(t.Context(), object.MeasuredAttributesID.Int64)
	if err != nil {
		t.Fatalf("GetAttributes: %v", err)
	}
	if attrs.AudioCodec.String != "flac" || !attrs.Lossless.Valid || attrs.Lossless.Int64 != 1 {
		t.Errorf("interned attributes = %+v, want audio_codec=flac lossless=true", attrs)
	}

	// A second run must find nothing left to probe -- the whole point of
	// ListUnmeasured's WHERE clause.
	files, err := physical.ListUnmeasured(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("ListUnmeasured: %v", err)
	}
	if len(files) != 0 {
		t.Errorf("ListUnmeasured after Run = %+v, want none left", files)
	}
}

func TestJob_RunSurvivesAMissingRootFolder(t *testing.T) {
	opened := newLibrary(t)
	physical := library.NewPhysical(opened)

	if _, err := physical.RegisterBatch(t.Context(), 99, []library.PhysicalFile{
		{RelPath: "orphan.flac", SizeBytes: 10, QuickHash: "hash-2", FirstSeenAt: time.Unix(0, 0)},
	}); err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}

	job := probe.NewJob(probe.Config{
		Physical: physical,
		Coverage: library.NewProbeCoverage(opened, clock.NewFakeAtEpoch()),
		Roots:    fakeRoots{}, // root 99 is not configured
		Tool:     &fakeTool{},
		Clock:    clock.NewFakeAtEpoch(),
		Logger:   slog.New(slog.DiscardHandler),
	})

	// A file whose root vanished must not fail the whole sweep.
	if err := job.Run(t.Context(), jobs.Job{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// slowTool is fakeTool taking a minute of the fake clock per file.
type slowTool struct {
	fakeTool
	clock *clock.Fake
}

func (s *slowTool) ReadTags(ctx context.Context, paths []string) ([]mediatool.Probe, error) {
	s.clock.Advance(time.Minute)
	return s.fakeTool.ReadTags(ctx, paths)
}

// A first sweep of a large library must not hold the one-slot maintenance pool for hours: after
// a batch past its time budget the run yields, keeping what it measured.
func TestJob_RunYieldsOnceItsTimeIsSpent(t *testing.T) {
	opened := newLibrary(t)
	physical := library.NewPhysical(opened)
	root := t.TempDir()
	files := make([]library.PhysicalFile, 150)
	for index := range files {
		name := fmt.Sprintf("track-%03d.flac", index)
		if err := os.WriteFile(filepath.Join(root, name), []byte("faked"), 0o644); err != nil {
			t.Fatalf("write fixture file: %v", err)
		}
		files[index] = library.PhysicalFile{RelPath: name, SizeBytes: 5, QuickHash: name, FirstSeenAt: time.Unix(0, 0)}
	}
	if _, err := physical.RegisterBatch(t.Context(), 1, files); err != nil {
		t.Fatalf("RegisterBatch: %v", err)
	}
	fake := clock.NewFakeAtEpoch()
	job := probe.NewJob(probe.Config{
		Physical: physical,
		Coverage: library.NewProbeCoverage(opened, fake),
		Roots:    fakeRoots{1: settings.RootFolder{ID: 1, Path: root, Online: true}},
		Tool: &slowTool{clock: fake, fakeTool: fakeTool{measured: mediatool.Measured{
			FormatName: "flac", AudioCodec: "flac", SampleRate: 44100, Duration: time.Second,
		}}},
		Clock:  fake,
		Logger: slog.New(slog.DiscardHandler),
	})

	if err := job.Run(t.Context(), jobs.Job{}); !errors.Is(err, jobs.ErrYield) {
		t.Fatalf("Run = %v, want it to yield", err)
	}
	left, err := physical.ListUnmeasured(t.Context(), 0, 200)
	if err != nil {
		t.Fatalf("ListUnmeasured: %v", err)
	}
	if len(left) != 50 {
		t.Errorf("%d files left unmeasured, want the 50 past the first batch", len(left))
	}
}
