package adapters_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Every kind is offered under the *Arr's own implementation name, so a client read from Sonarr or Lidarr is known.
func TestDownloadClientDefinitions_AreEveryKindUnderItsArrName(t *testing.T) {
	var impls []string
	for _, definition := range adapters.DownloadClientDefinitions() {
		impls = append(impls, definition.Impl)
		if definition.Label == "" || definition.Protocol == "" {
			t.Errorf("%s: label %q, protocol %q", definition.Impl, definition.Label, definition.Protocol)
		}
		if !definition.HasAddress() && len(definition.Fields) == 0 {
			t.Errorf("%s has neither an address nor folders", definition.Impl)
		}
		driver, known := adapters.NewDownloadClient(definition.Impl, adapters.DownloadClientDeps{}, download.Connection{BaseURL: "http://client:1"})
		if !known || driver == nil {
			t.Errorf("%s is defined but not built", definition.Impl)
		}
	}
	want := []string{"sabnzbd", "nzbget", "usenetblackhole", "qbittorrent", "transmission", "deluge", "rtorrent", "utorrent",
		"vuze", "flood", "aria2", "torrentblackhole", "slskd"}
	if !reflect.DeepEqual(impls, want) {
		t.Errorf("impls = %v, want %v", impls, want)
	}
	if _, known := adapters.NewDownloadClient("putio", adapters.DownloadClientDeps{}, download.Connection{}); known {
		t.Error("an unknown impl was built")
	}
}

// A blackhole has no address: its test looks at its folders, through the filesystem seam.
func TestConnectionTester_TestsABlackholesFolders(t *testing.T) {
	fake := clock.NewFakeAtEpoch()
	mem := fsx.NewMem(fake)
	for _, folder := range []string{"/drop", "/done"} {
		if err := mem.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tester := sabTester(t, nil)
	tester.FS = mem
	got, err := tester.TestDownloaderConfig(t.Context(), "torrentblackhole", "", "", settings.DownloaderOptions{DropFolder: "/drop", WatchFolder: "/done"})
	if err != nil || !got.OK || got.DownloadFolder != "/done" || !strings.HasPrefix(got.Message, "Connected. Torrent Blackhole.") {
		t.Errorf("test = %+v, %v", got, err)
	}
	got, _ = tester.TestDownloaderConfig(t.Context(), "torrentblackhole", "", "", settings.DownloaderOptions{DropFolder: "/drop", WatchFolder: "/nowhere"})
	if got.OK {
		t.Errorf("a missing watch folder passed: %+v", got)
	}
}
