package linkjob_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/moving/linkjob"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

type fixture struct {
	fsys  *fsx.Mem
	store *store.Store
	clock *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatal(err)
	}
	memory := fsx.NewMem(fake)
	memory.SetReflinkSupported(false)
	return &fixture{fsys: memory, store: opened, clock: fake}
}

func (f *fixture) write(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := f.fsys.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := f.fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
}

func (f *fixture) register(t *testing.T, root int64, rel string, size int64) {
	t.Helper()
	_, err := library.NewPhysical(f.store).RegisterBatch(t.Context(), root, []library.PhysicalFile{
		{RelPath: rel, SizeBytes: size, QuickHash: "same", FirstSeenAt: f.clock.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestJob_LinksTheSecondPathOfAnObjectAndRemembersIt(t *testing.T) {
	fx := newFixture(t)
	content := bytes.Repeat([]byte("x"), 100)
	fx.write(t, "/music/a/one.flac", content)
	fx.write(t, "/music/b/one.flac", content)
	fx.register(t, 1, "a/one.flac", 100)
	fx.register(t, 1, "b/one.flac", 100)
	config := linkjob.Config{
		FS: fx.fsys, Links: library.NewObjectLinks(fx.store),
		Roots: func(int64) []linkjob.Root { return []linkjob.Root{{ID: 1, Path: "/music"}} },
	}

	preview, err := config.PlanFor(t.Context(), 5)
	if err != nil || len(preview.Links) != 1 || preview.ReclaimableBytes != 100 {
		t.Fatalf("PlanFor = %+v, %v", preview, err)
	}
	payload, _ := json.Marshal(linkjob.Payload{CategoryID: 5})
	if err := linkjob.NewJob(config).Run(t.Context(), jobs.Job{Payload: payload}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first, _ := fx.fsys.Stat("/music/a/one.flac")
	second, _ := fx.fsys.Stat("/music/b/one.flac")
	if first.Inode != second.Inode {
		t.Error("the two paths are still separate files")
	}
	paths, err := library.NewObjectLinks(fx.store).Shared(t.Context())
	if err != nil || len(paths) != 2 || paths[0].LinkKind != library.LinkPrimary || paths[1].LinkKind != library.LinkHardlink {
		t.Fatalf("Shared = %+v, %v; want the second path marked hardlink", paths, err)
	}
	preview, err = config.PlanFor(t.Context(), 5)
	if err != nil || len(preview.Links) != 0 {
		t.Errorf("planned again: %+v, %v; want nothing left", preview, err)
	}
}

func TestPlanFor_IgnoresRootsOutsideTheLibrary(t *testing.T) {
	fx := newFixture(t)
	fx.write(t, "/music/a.flac", []byte("data"))
	fx.write(t, "/other/a.flac", []byte("data"))
	fx.register(t, 1, "a.flac", 4)
	fx.register(t, 2, "a.flac", 4)
	config := linkjob.Config{
		FS: fx.fsys, Links: library.NewObjectLinks(fx.store),
		Roots: func(int64) []linkjob.Root { return []linkjob.Root{{ID: 1, Path: "/music"}} },
	}
	preview, err := config.PlanFor(t.Context(), 5)
	if err != nil || len(preview.Links) != 0 {
		t.Fatalf("PlanFor = %+v, %v; want nothing for a library with one of the two roots", preview, err)
	}
}
