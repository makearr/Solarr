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
	if err != nil || ref != "Heka - Zanuvode" {
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
	if err != nil || ref != "Album" || read(t, mem, "/drop/Album.magnet") != magnet {
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
		{ClientRef: "New Album", Name: "New Album", State: download.StateDownloading, SizeBytes: 1},
		{ClientRef: "Old Album", Name: "Old Album", State: download.StateCompleted, SizeBytes: 5, Path: "/done/Old Album"},
		{ClientRef: "Single", Name: "Single.flac", State: download.StateCompleted, SizeBytes: 3, Path: "/done/Single.flac"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Downloads =\n%+v\nwant\n%+v", got, want)
	}
}

func TestUsenet_AddWritesTheNZBAndRemoveDeletesEverything(t *testing.T) {
	base, mem, _ := folders(t)
	driver := download.NewUsenet(blackhole.Usenet{Folders: base})
	ref, err := driver.Add(t.Context(), download.AddRequest{Name: "Heka: Zanuvode", File: clienttest.NZB()})
	if err != nil || ref != "Heka Zanuvode" || read(t, mem, "/drop/Heka Zanuvode.nzb") != string(clienttest.NZB()) {
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
