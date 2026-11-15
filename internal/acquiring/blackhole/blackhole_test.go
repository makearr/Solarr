package blackhole_test

import (
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/blackhole"
	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func folders(t *testing.T) (blackhole.Folders, *fsx.Mem, *clock.Fake) {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	mem := fsx.NewMem(fake)
	for _, folder := range []string{"/drop", "/done"} {
		if err := mem.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return blackhole.Folders{FS: mem, Clock: fake, Drop: "/drop", Watch: "/done"}, mem, fake
}

func write(t *testing.T, mem *fsx.Mem, name, content string) {
	t.Helper()
	if err := fsx.WriteAtomic(mem, name, func(writer io.Writer) error { _, err := io.WriteString(writer, content); return err }); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, mem *fsx.Mem, name string) string {
	t.Helper()
	file, err := mem.Open(name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	return string(data)
}

// A torrent is named in the drop folder, and known, by the name its content is saved under.
func TestTorrent_AddWritesTheTorrentFileUnderItsContentName(t *testing.T) {
	base, mem, _ := folders(t)
	driver := download.NewTorrent(blackhole.Torrent{Folders: base})
	file := clienttest.TorrentFile("Heka - Zanuvode")
	ref, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka-Zanuvode-1988-FLAC", File: file})
	if err != nil || ref != "hekazanuvode" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if got := read(t, mem, "/drop/Heka - Zanuvode.torrent"); got != string(file) {
		t.Errorf("drop file = %q", got)
	}
}

func TestTorrent_AMagnetIsRefusedUnlessMagnetFilesAreSaved(t *testing.T) {
	base, mem, _ := folders(t)
	magnet := "magnet:?xt=urn:btih:" + "0123456789abcdef0123456789abcdef01234567" + "&dn=Album"
	if _, err := download.NewTorrent(blackhole.Torrent{Folders: base}).Add(t.Context(), download.AddRequest{File: []byte(magnet)}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("Add = %v, want ErrRefused", err)
	}
	ref, err := download.NewTorrent(blackhole.Torrent{Folders: base, SaveMagnetFiles: true}).Add(t.Context(), download.AddRequest{File: []byte(magnet)})
	if err != nil || ref != "album" || read(t, mem, "/drop/Album.magnet") != magnet {
		t.Errorf("Add = %q, %v", ref, err)
	}
}

// What is still changing is downloading; what has not changed for a minute is finished, at its watch-folder path.
func TestUsenet_DownloadsAreFinishedOnceTheirFilesSettle(t *testing.T) {
	base, mem, fake := folders(t)
	if err := mem.MkdirAll("/done/Old Album", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, mem, "/done/Old Album/01.flac", "12345")
	write(t, mem, "/done/Single.flac", "123")
	fake.Advance(2 * time.Minute)
	if err := mem.MkdirAll("/done/New Album", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, mem, "/done/New Album/01.flac", "1")

	got, err := download.NewUsenet(blackhole.Usenet{Folders: base}).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	want := []download.Download{
		{ClientRef: "newalbum", Name: "New Album", State: download.StateDownloading, SizeBytes: 1},
		{ClientRef: "oldalbum", Name: "Old Album", State: download.StateCompleted, SizeBytes: 5, Path: "/done/Old Album"},
		{ClientRef: "single", Name: "Single.flac", State: download.StateCompleted, SizeBytes: 3, Path: "/done/Single.flac"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

func TestUsenet_AddWritesTheNZBAndRemoveDeletesEverything(t *testing.T) {
	base, mem, _ := folders(t)
	driver := download.NewUsenet(blackhole.Usenet{Folders: base})
	ref, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka: Zanuvode", File: clienttest.NZB()})
	if err != nil || ref != "hekazanuvode" || read(t, mem, "/drop/Heka Zanuvode.nzb") != string(clienttest.NZB()) {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if err := mem.MkdirAll("/done/Heka Zanuvode/CD1", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, mem, "/done/Heka Zanuvode/CD1/01.flac", "1")
	if err := driver.Remove(t.Context(), ref, false); !errors.Is(err, download.ErrNotSupported) {
		t.Errorf("Remove without files = %v, want ErrNotSupported", err)
	}
	if err := driver.Remove(t.Context(), ref, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, gone := range []string{"/drop/Heka Zanuvode.nzb", "/done/Heka Zanuvode"} {
		if _, err := mem.Stat(gone); err == nil {
			t.Errorf("%s is still there", gone)
		}
	}
}

func TestProbe_RefusesAMissingFolder(t *testing.T) {
	base, _, _ := folders(t)
	probe, err := blackhole.Usenet{Folders: base}.Probe(t.Context())
	if err != nil || probe.DownloadFolder != "/done" {
		t.Errorf("Probe = %+v, %v", probe, err)
	}
	base.Watch = "/missing"
	if _, err := (blackhole.Usenet{Folders: base}).Probe(t.Context()); err == nil {
		t.Error("a missing watch folder passed")
	}
	base.Watch = ""
	if _, err := (blackhole.Torrent{Folders: base}).Probe(t.Context()); !errors.Is(err, blackhole.ErrNoFolder) {
		t.Errorf("no watch folder: %v, want ErrNoFolder", err)
	}
}

// A torrent's name comes from the indexer: one that climbs out of the folder is cleaned into a plain name, and a
// reference that is not a plain name never deletes anything outside the two folders.
func TestTorrent_ANameThatClimbsOutIsNeverFollowed(t *testing.T) {
	base, mem, _ := folders(t)
	if err := mem.MkdirAll("/library/Album", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, mem, "/library/Album/01.flac", "1")
	driver := download.NewTorrent(blackhole.Torrent{Folders: base})
	ref, err := driver.Add(t.Context(), download.AddRequest{Name: "Album", File: clienttest.TorrentFile("../library")})
	if err != nil || ref != "library" {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if err := driver.Remove(t.Context(), "../library", true); err != nil {
		t.Errorf("Remove of a reference that climbs out = %v, want it reduced to its key", err)
	}
	if _, err := mem.Stat("/library/Album/01.flac"); err != nil {
		t.Errorf("a file outside the blackhole's folders was deleted: %v", err)
	}
}

// SABnzbd and NZBGet rename a job as they like: punctuation dropped, ".1" for a name taken, a password suffix stripped.
func TestUsenet_ARenamedFolderIsStillTheDownload(t *testing.T) {
	base, mem, fake := folders(t)
	driver := download.NewUsenet(blackhole.Usenet{Folders: base})
	ref, err := driver.Add(t.Context(), download.AddRequest{Name: "A+B: Zanuvode (2020){{secret}}", File: clienttest.NZB()})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := mem.MkdirAll("/done/A B Zanuvode (2020).1", 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, mem, "/done/A B Zanuvode (2020).1/01.flac", "1")
	fake.Advance(2 * time.Minute)

	got, err := driver.Downloads(t.Context(), "")
	if err != nil || len(got) != 1 || got[0].ClientRef != ref || got[0].State != download.StateCompleted {
		t.Fatalf("Downloads = %+v, %v; want one finished download referenced %q", got, err, ref)
	}
}

func TestRefKey_TellsDifferentReleasesApart(t *testing.T) {
	base, _, _ := folders(t)
	driver := download.NewUsenet(blackhole.Usenet{Folders: base})
	first, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka Zanuvode 1988", File: clienttest.NZB()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka Zanuvode 1989", File: clienttest.NZB()})
	if err != nil || first == second {
		t.Fatalf("keys %q and %q, %v; want two different downloads", first, second, err)
	}
	// The same release again would be one job to the client.
	if _, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka: Zanuvode 1988", File: clienttest.NZB()}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("a second send of one release = %v, want ErrRefused", err)
	}
}

// countingFS counts Lstat calls, the cost of a poll over a share.
type countingFS struct {
	fsx.FS
	lstats int
}

func (c *countingFS) Lstat(name string) (fsx.FileInfo, error) {
	c.lstats++
	return c.FS.Lstat(name)
}

func TestScan_ASettledDownloadIsNotWalkedAgain(t *testing.T) {
	base, mem, fake := folders(t)
	for _, album := range []string{"One", "Two"} {
		if err := mem.MkdirAll("/done/"+album+"/CD1", 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, mem, "/done/"+album+"/CD1/01.flac", "1")
		write(t, mem, "/done/"+album+"/CD1/02.flac", "1")
	}
	fake.Advance(2 * time.Minute)
	counted := &countingFS{FS: mem}
	base.FS, base.Memo = counted, &blackhole.Memo{}
	driver := download.NewTorrent(blackhole.Torrent{Folders: base})

	if _, err := driver.Downloads(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	counted.lstats = 0
	if _, err := driver.Downloads(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if counted.lstats != 2 {
		t.Errorf("second scan made %d Lstat calls, want 2 (one per download)", counted.lstats)
	}

	write(t, mem, "/done/One/03.flac", "1")
	got, _ := driver.Downloads(t.Context(), "")
	for _, one := range got {
		if one.ClientRef == "one" && one.SizeBytes != 3 {
			t.Errorf("a changed download measured %d bytes, want 3", one.SizeBytes)
		}
	}
}
