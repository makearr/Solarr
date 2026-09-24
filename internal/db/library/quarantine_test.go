package library_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// putTestFile writes content at path, creating its full parent chain --
// unlike the package's own writeFile, which only ever creates flat files
// directly under /media and cannot support quarantine's own nested
// <root>/quarantine/<date>/ layout.
func putTestFile(t *testing.T, fsys *fsx.Mem, path string, content []byte) {
	t.Helper()
	if err := fsys.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll for %q: %v", path, err)
	}
	file, err := fsys.Create(path)
	if err != nil {
		t.Fatalf("Create(%q): %v", path, err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("Write(%q): %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(%q): %v", path, err)
	}
}

func TestQuarantinePut_MovesFileAndRecordsIt(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/track.flac", []byte("audio bytes"))

	entry, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/track.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if entry.SizeBytes != int64(len("audio bytes")) {
		t.Errorf("SizeBytes = %d, want %d", entry.SizeBytes, len("audio bytes"))
	}
	if entry.Reason != library.ReasonJunk {
		t.Errorf("Reason = %q, want %q", entry.Reason, library.ReasonJunk)
	}
	wantExpiry := fake.Now().Add(168 * time.Hour).Unix()
	if entry.ExpiresAt != wantExpiry {
		t.Errorf("ExpiresAt = %d, want %d", entry.ExpiresAt, wantExpiry)
	}

	if _, err := fsys.Stat("/media/root/track.flac"); err == nil {
		t.Error("original path still exists after Put")
	}
	if _, err := fsys.Stat(entry.QuarantinePath); err != nil {
		t.Errorf("quarantine path %q does not exist: %v", entry.QuarantinePath, err)
	}

	got, err := q.Get(t.Context(), entry.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != entry {
		t.Errorf("Get = %+v, want %+v", got, entry)
	}
}

func TestQuarantinePut_CrossDeviceDeletesDirectlyInstead(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	fsys.SetRenameSupported(false)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/track.flac", []byte("audio bytes"))

	_, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/track.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if !errors.Is(err, library.ErrCrossDevice) {
		t.Fatalf("Put error = %v, want ErrCrossDevice", err)
	}

	if _, statErr := fsys.Stat("/media/root/track.flac"); statErr == nil {
		t.Error("original file still exists -- cross-device Put must delete it directly")
	}
}

func TestQuarantineRestore_PutsFileBackAndMarksRow(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/track.flac", []byte("audio bytes"))
	entry, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/track.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	restored, err := q.Restore(t.Context(), entry.ID)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.RestoredAt == nil {
		t.Fatal("RestoredAt is nil after Restore")
	}

	if _, err := fsys.Stat("/media/root/track.flac"); err != nil {
		t.Errorf("original path missing after Restore: %v", err)
	}
	if _, err := fsys.Stat(entry.QuarantinePath); err == nil {
		t.Error("quarantine path still exists after Restore")
	}

	if _, err := q.Restore(t.Context(), entry.ID); !errors.Is(err, library.ErrAlreadyRestored) {
		t.Errorf("second Restore error = %v, want ErrAlreadyRestored", err)
	}
}

func TestQuarantineRestore_RefusesWhenOriginalPathIsOccupied(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/track.flac", []byte("audio bytes"))
	entry, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/track.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Something else now occupies the original path -- a real upgrade landing
	// there, in the shape this guards against.
	putTestFile(t, fsys, "/media/root/track.flac", []byte("a different file entirely"))

	if _, err := q.Restore(t.Context(), entry.ID); !errors.Is(err, library.ErrRestoreConflict) {
		t.Fatalf("Restore error = %v, want ErrRestoreConflict", err)
	}
	content, err := fsys.Open("/media/root/track.flac")
	if err != nil {
		t.Fatalf("open occupying file: %v", err)
	}
	buf := make([]byte, 64)
	n, _ := content.Read(buf)
	if string(buf[:n]) != "a different file entirely" {
		t.Error("the occupying file was overwritten by a refused restore")
	}
}

func TestQuarantinePurge_RemovesBytesAndRow(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/track.flac", []byte("audio bytes"))
	entry, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/track.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if err := q.Purge(t.Context(), entry.ID); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if _, err := fsys.Stat(entry.QuarantinePath); err == nil {
		t.Error("quarantine path still exists after Purge")
	}
	if _, err := q.Get(t.Context(), entry.ID); err == nil {
		t.Error("row still exists after Purge")
	}
}

func TestQuarantineSweep_PurgesExpiredEntries(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/a.flac", []byte("aaaa"))
	putTestFile(t, fsys, "/media/root/b.flac", []byte("bbbb"))

	shortLived, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/a.flac",
		Reason: library.ReasonJunk, Retain: 1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	longLived, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/b.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put b: %v", err)
	}

	fake.Advance(2 * time.Hour)

	result, err := q.Sweep(t.Context(), library.QuarantineConfig{}, nil, nil)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if result.Purged != 1 || result.BytesFreed != 4 {
		t.Errorf("Sweep result = %+v, want Purged=1 BytesFreed=4", result)
	}
	if _, err := q.Get(t.Context(), shortLived.ID); err == nil {
		t.Error("expired entry still exists after Sweep")
	}
	if _, err := q.Get(t.Context(), longLived.ID); err != nil {
		t.Error("unexpired entry was purged by Sweep")
	}
}

func TestQuarantineSweep_EvictsOldestFirstOverGlobalByteBudget(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	putTestFile(t, fsys, "/media/root/a.flac", []byte("aaaa"))
	fake.Advance(time.Minute)
	putTestFile(t, fsys, "/media/root/b.flac", []byte("bbbb"))

	oldest, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/a.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	newest, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/b.flac",
		Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put b: %v", err)
	}

	// Both entries total 8 bytes; a 4-byte budget must evict exactly the
	// older one and leave the newer one alone.
	result, err := q.Sweep(t.Context(), library.QuarantineConfig{MaxBytes: 4}, nil, nil)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if result.Purged != 1 || result.BytesFreed != 4 {
		t.Errorf("Sweep result = %+v, want Purged=1 BytesFreed=4", result)
	}
	if _, err := q.Get(t.Context(), oldest.ID); err == nil {
		t.Error("oldest entry survived a global-budget sweep")
	}
	if _, err := q.Get(t.Context(), newest.ID); err != nil {
		t.Error("newest entry was evicted before the oldest one")
	}
}

func TestQuarantineSweep_EvictsOverPerRootPercentBudget(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)

	rootID := int64(1)
	putTestFile(t, fsys, "/media/root/a.flac", []byte("aaaaaaaaaa")) // 10 bytes

	entry, err := q.Put(t.Context(), library.PutRequest{
		RootPath: "/media/root", OriginalPath: "/media/root/a.flac",
		RootFolderID: &rootID, Reason: library.ReasonJunk, Retain: 168 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// 10 bytes held against a 1000-byte root at a 0.5% budget (5 bytes) is over.
	capacity := func(id int64) (int64, bool) {
		if id == rootID {
			return 1000, true
		}
		return 0, false
	}
	result, err := q.Sweep(t.Context(), library.QuarantineConfig{MaxPercentOfRoot: 0.5}, capacity, []int64{rootID})
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if result.Purged != 1 {
		t.Errorf("Purged = %d, want 1", result.Purged)
	}
	if _, err := q.Get(t.Context(), entry.ID); err == nil {
		t.Error("entry survived a per-root-budget sweep")
	}
}

// A library's "Delete empty folders": the album folder goes, the artist folder that still holds
// another album stays, and so does the root.
func TestQuarantinePut_RemovesTheFoldersItEmptied(t *testing.T) {
	opened, _ := newLibrary(t)
	fake := clock.NewFakeAtEpoch()
	fsys := fsx.NewMem(fake)
	q := library.NewQuarantine(opened, fsys, fake)
	putTestFile(t, fsys, "/media/root/Heka/Watermark/03.flac", []byte("audio"))
	putTestFile(t, fsys, "/media/root/Heka/Detisisu Binip/01.flac", []byte("audio"))
	putTestFile(t, fsys, "/media/root/Solo/Only/01.flac", []byte("audio"))

	for _, original := range []string{"/media/root/Heka/Watermark/03.flac", "/media/root/Solo/Only/01.flac"} {
		if _, err := q.Put(t.Context(), library.PutRequest{
			RootPath: "/media/root", OriginalPath: original, Reason: library.ReasonUserDelete,
			Retain: time.Hour, RemoveEmptyFolders: true,
		}); err != nil {
			t.Fatalf("Put(%s): %v", original, err)
		}
	}
	for folder, wantExists := range map[string]bool{
		"/media/root/Heka/Watermark": false, "/media/root/Heka": true, "/media/root/Solo": false, "/media/root": true,
	} {
		_, err := fsys.Stat(folder)
		if exists := err == nil; exists != wantExists {
			t.Errorf("%s exists = %v, want %v", folder, exists, wantExists)
		}
	}
}
