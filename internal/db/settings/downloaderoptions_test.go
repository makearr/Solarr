package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestDownloaderOptions_MapsOnlyPathsUnderTheRemoteFolder(t *testing.T) {
	options := settings.Downloader{SettingsJSON: `{"remote_path":"/downloads/","local_path":"/storage/download"}`}.Options()
	for reported, want := range map[string]string{
		"/downloads/complete/Heka-Watermark": "/storage/download/complete/Heka-Watermark",
		"/downloads":                         "/storage/download",
		"/downloads-other/x":                 "/downloads-other/x",
		"/elsewhere/x":                       "/elsewhere/x",
	} {
		if got := options.LocalPathFor(reported); got != want {
			t.Errorf("LocalPathFor(%q) = %q, want %q", reported, got, want)
		}
	}
	if !options.RemovesCompleted() {
		t.Error("RemovesCompleted default = false, want true")
	}
}
