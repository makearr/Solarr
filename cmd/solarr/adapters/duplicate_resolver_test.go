package adapters_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func TestResolveDuplicatesEverywhere_ACopyInAnotherLibraryIsNeverMerged(t *testing.T) {
	cache := newSweepCache(t)
	var roots []int64
	for _, name := range []string{"Films", "Docs"} {
		category, err := cache.CreateCategory(t.Context(), "movie", name, 1)
		if err != nil {
			t.Fatalf("CreateCategory %s: %v", name, err)
		}
		root, err := cache.CreateRootFolder(t.Context(), "/media/"+strings.ToLower(name), name)
		if err != nil {
			t.Fatalf("CreateRootFolder %s: %v", name, err)
		}
		if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{CategoryID: category.ID, RootFolderID: root.ID, AcceptsNew: true}); err != nil {
			t.Fatalf("AttachRootFolder %s: %v", name, err)
		}
		roots = append(roots, root.ID)
	}

	directory := t.TempDir()
	opened, err := store.Open(t.Context(), clock.NewFakeAtEpoch(), store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"), Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	physical := library.NewPhysical(opened)
	keep, err := physical.RegisterBatch(t.Context(), roots[0], []library.PhysicalFile{{RelPath: "a.mkv", SizeBytes: 100, QuickHash: "a", FirstSeenAt: time.Unix(0, 0)}})
	if err != nil {
		t.Fatalf("RegisterBatch keep: %v", err)
	}
	other, err := physical.RegisterBatch(t.Context(), roots[1], []library.PhysicalFile{{RelPath: "b.mkv", SizeBytes: 90, QuickHash: "b", FirstSeenAt: time.Unix(0, 0)}})
	if err != nil {
		t.Fatalf("RegisterBatch other: %v", err)
	}

	resolver := adapters.DuplicateResolver{Physical: physical, Settings: cache}
	outcome, err := resolver.ResolveDuplicatesEverywhere(t.Context(), keep[0].MediaObjectID, []apiserver.DuplicateDrop{{MediaObjectID: other[0].MediaObjectID, Action: "link"}})
	if err != nil {
		t.Fatalf("ResolveDuplicatesEverywhere: %v", err)
	}
	if len(outcome.Problems) != 1 || !strings.Contains(outcome.Problems[0], "another library") || outcome.Linked != 0 || outcome.Recycled != 0 {
		t.Errorf("outcome = %+v, want the one copy refused for being in another library", outcome)
	}
}
