package settings_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestDownloaderOptions_MapsOnlyPathsUnderTheRemoteFolder(t *testing.T) {
	options := settings.Downloader{SettingsJSON: `{"remote_path":"/downloads/","local_path":"/storage/download"}`}.Options()
	for reported, want := range map[string]string{
		"/downloads/complete/Heka-Zanuvode": "/storage/download/complete/Heka-Zanuvode",
		"/downloads":                        "/storage/download",
		"/downloads-other/x":                "/downloads-other/x",
		"/elsewhere/x":                      "/elsewhere/x",
	} {
		if got := options.LocalPathFor(reported); got != want {
			t.Errorf("LocalPathFor(%q) = %q, want %q", reported, got, want)
		}
	}
	if !options.RemovesCompleted() {
		t.Error("RemovesCompleted default = false, want true")
	}
}

// Solarr's own category by default, never an *Arr's ("music"), unless the client names another.
func TestDownloaderOptions_CategoryDefaultsToSolarrsOwn(t *testing.T) {
	got := []string{
		settings.Downloader{}.Options().CategoryFor("music"),
		settings.Downloader{SettingsJSON: `{"category":"lidarr"}`}.Options().CategoryFor("music"),
	}
	want := []string{"solarr-music", "lidarr"}
	if !slices.Equal(got, want) {
		t.Errorf("CategoryFor = %v, want %v", got, want)
	}
}
