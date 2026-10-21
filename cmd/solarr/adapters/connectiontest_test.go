package adapters_test

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func TestClientCategories_IsOnePerMediaTypeUnlessTheClientNamesItsOwn(t *testing.T) {
	libraries := []string{"music", "tv", "tv"}
	cases := []struct {
		name    string
		options settings.DownloaderOptions
		types   []string
		want    []string
	}{
		{"each media type under Solarr's own name", settings.DownloaderOptions{}, libraries, []string{"solarr-music", "solarr-tv"}},
		{"the client's own category for everything", settings.DownloaderOptions{Category: "solarr"}, libraries, []string{"solarr"}},
		{"no library, nothing to check", settings.DownloaderOptions{}, nil, nil},
	}
	for _, test := range cases {
		if got := adapters.ClientCategories(test.options, test.types); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: ClientCategories = %v, want %v", test.name, got, test.want)
		}
	}
}

// fakeSABnzbd answers the three calls a connection test makes.
type fakeSABnzbd struct{}

func (fakeSABnzbd) RoundTrip(request *http.Request) (*http.Response, error) {
	body := map[string]string{
		"version":    `{"version":"4.3.0"}`,
		"get_config": `{"config":{"misc":{"complete_dir":"/downloads"}}}`,
		"get_cats":   `{"categories":["*","music","solarr-music"]}`,
	}[request.URL.Query().Get("mode")]
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
}

func sabTester(t *testing.T, shared map[string]string) adapters.ConnectionTester {
	t.Helper()
	cache := newSweepCache(t)
	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 10); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	fake := clock.NewFakeAtEpoch()
	return adapters.ConnectionTester{
		Cache: cache, Clock: fake,
		Downloader: func(int64, string) *httpx.Client {
			return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil),
				httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter),
				httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute}),
				httpx.ClientConfig{Provider: "sabnzbd-test", UserAgent: "test", Transport: fakeSABnzbd{}})
		},
		SharedCategories: func(context.Context, string) map[string]string { return shared },
	}
}

// A category Lidarr also downloads into fails the test: Lidarr would import Solarr's downloads.
func TestConnectionTester_FailsACategoryAConnectedArrAlsoUses(t *testing.T) {
	tester := sabTester(t, map[string]string{"music": "Home Lidarr"})
	tests := []struct {
		category string
		want     apiserver.ConnectionTestResult
	}{
		{"music", apiserver.ConnectionTestResult{
			Message: `Connected. Version 4.3.0. Finished downloads go to /downloads. Home Lidarr also downloads into the "music"` +
				` category on this client and would import what Solarr sends there: give Solarr a category of its own.`,
		}},
		{"", apiserver.ConnectionTestResult{OK: true, Message: "Connected. Version 4.3.0. Finished downloads go to /downloads."}},
	}
	for _, test := range tests {
		got, err := tester.TestDownloaderConfig(t.Context(), adapters.ImplSABnzbd, "http://sab:8080", "key",
			settings.DownloaderOptions{Category: test.category})
		if err != nil {
			t.Fatal(err)
		}
		test.want.TestedAt, test.want.DownloadFolder = got.TestedAt, "/downloads"
		if got != test.want {
			t.Errorf("category %q: got %+v, want %+v", test.category, got, test.want)
		}
	}
}

func TestSameClientAddress_ComparesHostAndPortOnly(t *testing.T) {
	cases := map[[2]string]bool{
		{"http://10.0.0.5:8080/sab", "https://10.0.0.5:8080"}: true,
		{"http://SAB:8080", "http://sab:8080/"}:               true,
		{"http://sab:8080", "http://sab:8081"}:                false,
		{"", ""}:                                              false,
	}
	for pair, want := range cases {
		if got := adapters.SameClientAddress(pair[0], pair[1]); got != want {
			t.Errorf("SameClientAddress(%q, %q) = %v, want %v", pair[0], pair[1], got, want)
		}
	}
}
