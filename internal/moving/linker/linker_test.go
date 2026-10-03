package linker_test

import (
	"bytes"
	"errors"
	"io"
	"path"
	"testing"

	"github.com/makearr/Solarr/internal/moving/linker"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func put(t *testing.T, fsys fsx.FS, name string, content []byte) {
	t.Helper()
	if err := fsys.MkdirAll(path.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func object(size int, paths ...string) linker.Object {
	built := linker.Object{MediaObjectID: 7, SizeBytes: int64(size)}
	for index, p := range paths {
		built.Paths = append(built.Paths, linker.Path{PathID: int64(index + 1), RootFolderID: 1, Abs: p})
	}
	return built
}

func TestPlanAndApply_ReplaceTheSecondCopyWithAHardlink(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	content := bytes.Repeat([]byte("abc"), 1000)
	put(t, fsys, "/a/one.flac", content)
	put(t, fsys, "/b/one.flac", content)
	fsys.SetReflinkSupported(false)

	links, skipped, err := linker.Plan(fsys, []linker.Object{object(len(content), "/a/one.flac", "/b/one.flac")})
	if err != nil || len(skipped) != 0 || len(links) != 1 {
		t.Fatalf("Plan = %+v, %+v, %v", links, skipped, err)
	}
	if links[0].Keep.Abs != "/a/one.flac" || links[0].Replace.Abs != "/b/one.flac" || links[0].Method != linker.Hardlink {
		t.Errorf("link = %+v", links[0])
	}
	if _, err := fsys.Stat("/b/one.flac.solarr-probe"); err == nil {
		t.Error("the probe left a file behind")
	}
	method, err := linker.Apply(fsys, links[0])
	if err != nil || method != linker.Hardlink {
		t.Fatalf("Apply = %v, %v", method, err)
	}
	first, _ := fsys.Stat("/a/one.flac")
	second, _ := fsys.Stat("/b/one.flac")
	if first.Inode != second.Inode {
		t.Errorf("inodes %d and %d: not one file", first.Inode, second.Inode)
	}
	if _, err := fsys.Stat("/b/one.flac.solarr-link-tmp"); err == nil {
		t.Error("the temporary link was left behind")
	}
	// Planned again, nothing is left to do.
	links, _, err = linker.Plan(fsys, []linker.Object{object(len(content), "/a/one.flac", "/b/one.flac")})
	if err != nil || len(links) != 0 {
		t.Errorf("second Plan = %+v, %v; want none", links, err)
	}
}

func TestApply_RefusesFilesThatDifferAndLeavesBothUntouched(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fsys, "/a/x", []byte("aaaa"))
	put(t, fsys, "/b/x", []byte("aaab"))
	links, _, err := linker.Plan(fsys, []linker.Object{object(4, "/a/x", "/b/x")})
	if err != nil || len(links) != 1 {
		t.Fatalf("Plan = %+v, %v", links, err)
	}
	if _, err := linker.Apply(fsys, links[0]); !errors.Is(err, linker.ErrContentDiffers) {
		t.Fatalf("Apply error = %v, want ErrContentDiffers", err)
	}
	file, _ := fsys.Open("/b/x")
	got, _ := io.ReadAll(file)
	if string(got) != "aaab" {
		t.Errorf("the replacement changed: %q", got)
	}
}

func TestPlan_ReportsAMissingPathAndAChangedSize(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fsys, "/a/x", []byte("aaaa"))
	put(t, fsys, "/b/x", []byte("aaaaa"))
	links, skipped, err := linker.Plan(fsys, []linker.Object{object(4, "/a/x", "/b/x", "/c/x")})
	if err != nil || len(links) != 0 {
		t.Fatalf("Plan = %+v, %v", links, err)
	}
	reasons := map[string]string{}
	for _, one := range skipped {
		reasons[one.Path.Abs] = one.Reason
	}
	if reasons["/b/x"] != linker.ReasonSizeChanged || reasons["/c/x"] != linker.ReasonMissing {
		t.Errorf("skipped = %v", reasons)
	}
}

func TestPlan_FallsBackToReflinkOnlyWhenTheFilesystemOffersIt(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fsys, "/a/x", []byte("data"))
	put(t, fsys, "/b/x", []byte("data"))
	fsys.SetHardlinkSupported(false)
	links, _, err := linker.Plan(fsys, []linker.Object{object(4, "/a/x", "/b/x")})
	if err != nil || len(links) != 1 || links[0].Method != linker.Reflink {
		t.Fatalf("Plan = %+v, %v; want a reflink", links, err)
	}
	fsys.SetReflinkSupported(false)
	links, skipped, err := linker.Plan(fsys, []linker.Object{object(4, "/a/x", "/b/x")})
	if err != nil || len(links) != 0 || len(skipped) != 1 || skipped[0].Reason != linker.ReasonNoMethod {
		t.Fatalf("Plan = %+v, %+v, %v; want no method", links, skipped, err)
	}
}

// The real filesystem reports inodes, so a hardlink is recognised as one file and not planned again.
func TestApply_OnTheRealFilesystemSharesOneInode(t *testing.T) {
	fsys := fsx.OS{}
	directory := t.TempDir()
	content := bytes.Repeat([]byte("real"), 500)
	put(t, fsys, path.Join(directory, "a", "x.flac"), content)
	put(t, fsys, path.Join(directory, "b", "x.flac"), content)
	objects := []linker.Object{object(len(content), path.Join(directory, "a", "x.flac"), path.Join(directory, "b", "x.flac"))}

	links, _, err := linker.Plan(fsys, objects)
	if err != nil || len(links) != 1 {
		t.Fatalf("Plan = %+v, %v", links, err)
	}
	if _, err := linker.Apply(fsys, links[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	first, _ := fsys.Stat(path.Join(directory, "a", "x.flac"))
	second, _ := fsys.Stat(path.Join(directory, "b", "x.flac"))
	if first.Inode == 0 || first.Inode != second.Inode {
		t.Errorf("inodes %d and %d, want one file", first.Inode, second.Inode)
	}
	if links, _, err = linker.Plan(fsys, objects); err != nil || len(links) != 0 {
		t.Errorf("second Plan = %+v, %v; want none", links, err)
	}
}

func TestMake_PutsALinkWhereTheOldFileWasMovedAway(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	put(t, fsys, "/a/keep.flac", []byte("kept bytes"))
	if err := fsys.MkdirAll("/b", 0o755); err != nil {
		t.Fatal(err)
	}
	fsys.SetReflinkSupported(false)

	method, err := linker.Make(fsys, "/a/keep.flac", "/b/other.flac")
	if err != nil || method != linker.Hardlink {
		t.Fatalf("Make = %q, %v; want a hardlink", method, err)
	}
	file, err := fsys.Open("/b/other.flac")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	got, _ := io.ReadAll(file)
	if string(got) != "kept bytes" {
		t.Errorf("linked file holds %q, want the kept file's bytes", got)
	}
}
