package adapters

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

func writeMem(t *testing.T, fsys *fsx.Mem, path, content string) {
	t.Helper()
	if err := fsys.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// A link that fails at the third path leaves all three originals where they were and nothing active in the bin (DUP-1).
func TestDuplicateResolver_AFailedLinkPutsEveryOriginalBack(t *testing.T) {
	directory := t.TempDir()
	fake := clock.NewFakeAtEpoch()
	if err := os.MkdirAll(filepath.Join(directory, "backups"), 0o750); err != nil {
		t.Fatal(err)
	}
	opened, err := store.Open(t.Context(), fake, store.Config{Name: "library", Path: filepath.Join(directory, "library.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	owner, err := migrations.Library()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context(), opened, filepath.Join(directory, "backups"), owner); err != nil {
		t.Fatal(err)
	}
	fsys := fsx.NewMem(fake)
	bin := library.NewRecycleBin(opened, fsys, fake)
	resolver := DuplicateResolver{FS: fsys, Recycle: bin}

	var swapped []library.Entry
	for _, name := range []string{"one", "two", "three"} {
		original := "/media/root/" + name + ".flac"
		writeMem(t, fsys, original, "original "+name)
		entry, err := bin.Put(t.Context(), library.PutRequest{RootPath: "/media/root", OriginalPath: original, Reason: library.ReasonReplaced, Retain: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		if name != "three" { // the third link is the one that fails, so nothing took its place
			writeMem(t, fsys, original, "link to the kept copy")
		}
		swapped = append(swapped, entry)
	}
	cause := errors.New("cross-device link")
	if err := resolver.rollBack(t.Context(), swapped, cause); !errors.Is(err, cause) {
		t.Fatalf("rollBack = %v, want the cause", err)
	}
	for _, name := range []string{"one", "two", "three"} {
		file, err := fsys.Open("/media/root/" + name + ".flac")
		if err != nil {
			t.Fatalf("%s is not back: %v", name, err)
		}
		got, _ := io.ReadAll(file)
		_ = file.Close()
		if string(got) != "original "+name {
			t.Errorf("%s holds %q, want its original bytes", name, got)
		}
	}
	if _, total, _, err := bin.List(t.Context()); err != nil || total != 0 {
		t.Errorf("active bin entries = %d, %v; want none", total, err)
	}
}
