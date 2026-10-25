package download_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
)

// fakeTorrent records what the torrent base asked of it.
type fakeTorrent struct {
	file, magnet string
	paused       int
	downloads    []download.Download
}

func (f *fakeTorrent) AddTorrentFile(_ context.Context, hash string, _ download.AddRequest) (string, error) {
	f.file = hash
	return hash, nil
}

func (f *fakeTorrent) AddMagnet(_ context.Context, hash, magnet string, _ download.AddRequest) (string, error) {
	f.magnet = magnet
	return hash, nil
}

func (f *fakeTorrent) Downloads(context.Context, string) ([]download.Download, error) {
	return f.downloads, nil
}
func (f *fakeTorrent) Remove(context.Context, string, bool) error    { return nil }
func (f *fakeTorrent) Pause(context.Context, []string) error         { f.paused++; return nil }
func (f *fakeTorrent) Resume(context.Context, []string) error        { return nil }
func (f *fakeTorrent) Probe(context.Context) (download.Probe, error) { return download.Probe{}, nil }

type finishingTorrent struct{ *fakeTorrent }

func (finishingTorrent) FinishedFiles(context.Context, string) (string, []string, error) {
	return "/downloads/a", []string{"01.flac"}, nil
}

const albumTorrent = "d8:announce3:url4:infod6:lengthi5e4:name5:Album12:piece lengthi16384e6:pieces20:01234567890123456789ee"

func TestTorrentBase_SendsAFileOrAMagnetByWhatTheReleaseIs(t *testing.T) {
	client := &fakeTorrent{}
	driver := download.NewTorrent(client)
	ref, err := driver.Add(t.Context(), download.AddRequest{File: []byte(albumTorrent)})
	if err != nil || ref != client.file || len(ref) != 40 || client.magnet != "" {
		t.Fatalf("a torrent file: ref %q, %v, client %+v", ref, err, client)
	}
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Album"
	if ref, err := driver.Add(t.Context(), download.AddRequest{File: []byte(magnet + "\n")}); err != nil ||
		ref != "0123456789abcdef0123456789abcdef01234567" || client.magnet != magnet {
		t.Errorf("a magnet: ref %q, %v, client %+v", ref, err, client)
	}
	if _, err := driver.Add(t.Context(), download.AddRequest{File: []byte("<html>")}); !errors.Is(err, download.ErrNoInfoHash) {
		t.Errorf("not a torrent: %v, want ErrNoInfoHash", err)
	}
}

func TestTorrentBase_NeverAsksAClientToPauseNothing(t *testing.T) {
	client := &fakeTorrent{}
	if err := download.NewTorrent(client).Pause(t.Context(), nil); err != nil || client.paused != 0 {
		t.Errorf("Pause(nil) = %v, client asked %d times", err, client.paused)
	}
}

func TestTorrentBase_QueueStatusCountsOnlyWhatIsStillComing(t *testing.T) {
	client := &fakeTorrent{downloads: []download.Download{
		{State: download.StateDownloading, SizeLeftBytes: 4, SpeedBytesPerSec: 10},
		{State: download.StatePaused, SizeLeftBytes: 6},
		{State: download.StateFailed, SizeLeftBytes: 9},
		{State: download.StateCompleted, SpeedBytesPerSec: 1},
	}}
	got, err := download.NewTorrent(client).QueueStatus(t.Context())
	if want := (download.QueueStatus{Items: 2, SizeLeftBytes: 10, SpeedBytesPerSec: 11}); err != nil || got != want {
		t.Errorf("QueueStatus = %+v, %v; want %+v", got, err, want)
	}
}

// Only a client that can name finished files keeps download.FinishedFiles through the base.
func TestTorrentBase_KeepsFinishedFilesOnlyWhereTheClientHasThem(t *testing.T) {
	if _, ok := download.NewTorrent(&fakeTorrent{}).(download.FinishedFiles); ok {
		t.Error("a client without FinishedFiles gained it")
	}
	finisher, ok := download.NewTorrent(finishingTorrent{&fakeTorrent{}}).(download.FinishedFiles)
	if !ok {
		t.Fatal("a client with FinishedFiles lost it")
	}
	if folder, files, err := finisher.FinishedFiles(t.Context(), "x"); err != nil || folder != "/downloads/a" || len(files) != 1 {
		t.Errorf("FinishedFiles = %q %q %v", folder, files, err)
	}
}

func TestTorrentPath_IsTheFolderOfAMultiFileTorrentAndTheFileOfASingleOne(t *testing.T) {
	cases := map[[2]string]string{
		{"/downloads/Album", "Album"}:    "/downloads/Album",
		{"/downloads/", "track.flac"}:    "/downloads/track.flac",
		{`C:\Downloads`, "track.flac"}:   `C:\Downloads\track.flac`,
		{`C:\Downloads\Album\`, "Album"}: `C:\Downloads\Album`,
		{"", "x"}:                        "",
	}
	for input, want := range cases {
		if got := download.TorrentPath(input[0], input[1]); got != want {
			t.Errorf("TorrentPath(%q, %q) = %q, want %q", input[0], input[1], got, want)
		}
	}
}

func TestTorrentName_ReadsTheInfoNameOrTheMagnetsDisplayName(t *testing.T) {
	if got := download.TorrentName([]byte(albumTorrent)); got != "Album" {
		t.Errorf("torrent file: %q", got)
	}
	if got := download.TorrentName([]byte("magnet:?xt=urn:btih:abc&dn=Heka+-+Zanuvode")); got != "Heka - Zanuvode" {
		t.Errorf("magnet: %q", got)
	}
	if got := download.TorrentName([]byte("not a torrent")); got != "" {
		t.Errorf("junk: %q", got)
	}
}

func TestParseDirectRelease_NeedsAPeerAndFiles(t *testing.T) {
	release, err := download.ParseDirectRelease([]byte(`{"source":"peer","files":[{"path":"a\\b.flac","size":3}]}`))
	if err != nil || release.Source != "peer" || len(release.Files) != 1 || release.Files[0].Size != 3 {
		t.Errorf("ParseDirectRelease = %+v, %v", release, err)
	}
	for _, bad := range []string{`{"files":[{"path":"x"}]}`, `{"source":"peer","files":[]}`, `<nzb>`} {
		if _, err := download.ParseDirectRelease([]byte(bad)); !errors.Is(err, download.ErrNotDirectRelease) {
			t.Errorf("%s: %v, want ErrNotDirectRelease", bad, err)
		}
	}
}
