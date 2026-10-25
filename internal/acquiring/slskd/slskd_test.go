package slskd_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/clienttest"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/slskd"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

func driver(t *testing.T, server *clienttest.Server) download.Driver {
	t.Helper()
	server.Answer = func(request clienttest.Request) (clienttest.Route, bool) {
		if request.Header.Get("X-API-Key") != "key" {
			return clienttest.Route{Status: http.StatusUnauthorized}, true
		}
		return clienttest.Route{}, false
	}
	return download.NewDirect(slskd.Downloader{Client: slskd.NewClient(httpClient(t, server), "http://slskd:5030", "key")})
}

const release = `{"source":"peer one","whole_folder":true,"files":[{"path":"@@music\\Heka\\Zanuvode\\01.flac","size":10},{"path":"@@music\\Heka\\Zanuvode\\02.flac","size":20}]}`

func TestDownloader_AddAsksThePeerForTheFolder(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"POST /api/v0/transfers/downloads/peer one": {Status: http.StatusCreated}}}
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(release)})
	if err != nil || ref != `peer%20one|@@music\Heka\Zanuvode` {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if body := server.Seen[0].Body; body != `[{"filename":"@@music\\Heka\\Zanuvode\\01.flac","size":10},{"filename":"@@music\\Heka\\Zanuvode\\02.flac","size":20}]` {
		t.Errorf("body = %s", body)
	}
	if _, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(`{"files":[]}`)}); !errors.Is(err, download.ErrNotDirectRelease) {
		t.Errorf("an empty release: %v, want ErrNotDirectRelease", err)
	}
}

func TestDownloader_DownloadsIsOnePerFolderWithAPartPerFile(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/options": {Body: `{"directories":{"downloads":"/app/downloads"}}`},
		"/api/v0/transfers/downloads": {Body: `[{"username":"peer","directories":[
		 {"directory":"@@music\\A\\Done","files":[{"id":"1","filename":"@@music\\A\\Done\\01.flac","state":"Completed, Succeeded","size":10},
		  {"id":"2","filename":"@@music\\A\\Done\\02.flac","state":"Completed, Succeeded","size":5}]},
		 {"directory":"@@video\\Films","files":[{"id":"3","filename":"@@video\\Films\\Film.2001.mkv","state":"Completed, Succeeded","size":100},
		  {"id":"4","filename":"@@video\\Films\\Film.2001.en.srt","state":"InProgress","size":2,"bytesRemaining":1,"averageSpeed":8},
		  {"id":"5","filename":"@@video\\Films\\Other.mkv","state":"Completed, Succeeded","size":50}]},
		 {"directory":"@@music\\A\\Bad","files":[{"id":"6","filename":"@@music\\A\\Bad\\x.flac","state":"Completed, Rejected","size":10,"bytesRemaining":10,"exception":"File not shared."}]}]}]`},
	}}
	got, err := driver(t, server).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var wholes []download.Download
	parts := map[string]download.Download{}
	for _, one := range got {
		if one.Part {
			parts[one.ClientRef] = one
		} else {
			wholes = append(wholes, one)
		}
	}
	want := []download.Download{
		{ClientRef: `peer|@@music\A\Done`, Name: "Done", State: download.StateCompleted, SizeBytes: 15, Path: "/app/downloads/Done"},
		{ClientRef: `peer|@@video\Films`, Name: "Films", State: download.StateDownloading, SizeBytes: 152, SizeLeftBytes: 1, SpeedBytesPerSec: 8},
		{ClientRef: `peer|@@music\A\Bad`, Name: "Bad", State: download.StateFailed, SizeBytes: 10, SizeLeftBytes: 10,
			Error: "slskd reports Completed, Rejected: File not shared."},
	}
	if !reflect.DeepEqual(wholes, want) {
		t.Errorf("folders =\n%+v\nwant\n%+v", wholes, want)
	}
	// The film waits for its subtitles; the other film in the folder is done on its own.
	film, other := parts[`peer|@@video\Films|Film.2001.mkv`], parts[`peer|@@video\Films|Other.mkv`]
	if film.State != download.StateDownloading || film.SizeBytes != 102 || other.State != download.StateCompleted ||
		other.Path != "/app/downloads/Films/Other.mkv" {
		t.Errorf("parts: film %+v, other %+v", film, other)
	}
	status, err := driver(t, server).QueueStatus(t.Context())
	if err != nil || status.Items != 1 || status.SizeLeftBytes != 1 {
		t.Errorf("QueueStatus = %+v, %v; want the folder counted once, its parts not", status, err)
	}
}

// A release of one file is followed, and removed, by that file and what shares its name.
func TestDownloader_AFileReleaseIsItsOwnDownload(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"POST /api/v0/transfers/downloads/peer": {Status: http.StatusCreated},
		"/api/v0/transfers/downloads": {Body: `[{"username":"peer","directories":[{"directory":"@@video\\Films","files":[
		 {"id":"3","filename":"@@video\\Films\\Film.2001.mkv"},{"id":"4","filename":"@@video\\Films\\Film.2001.en.srt"},
		 {"id":"5","filename":"@@video\\Films\\Other.mkv"}]}]}]`},
		"DELETE /api/v0/transfers/downloads/peer/3": {}, "DELETE /api/v0/transfers/downloads/peer/4": {},
		"DELETE /api/v0/files/downloads/files/RmlsbXMvRmlsbS4yMDAxLm1rdg==":     {},
		"DELETE /api/v0/files/downloads/files/RmlsbXMvRmlsbS4yMDAxLmVuLnNydA==": {},
	}}
	release := `{"source":"peer","files":[{"path":"@@video\\Films\\Film.2001.mkv","size":100},{"path":"@@video\\Films\\Film.2001.en.srt","size":2}]}`
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(release)})
	if err != nil || ref != `peer|@@video\Films|Film.2001.mkv` {
		t.Fatalf("Add = %q, %v", ref, err)
	}
	if err := driver(t, server).Remove(t.Context(), ref, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var deleted []string
	for _, seen := range server.Seen {
		if seen.Method == http.MethodDelete {
			deleted = append(deleted, seen.Path)
		}
	}
	want := []string{"/api/v0/transfers/downloads/peer/3", "/api/v0/transfers/downloads/peer/4",
		"/api/v0/files/downloads/files/RmlsbXMvRmlsbS4yMDAxLm1rdg==", "/api/v0/files/downloads/files/RmlsbXMvRmlsbS4yMDAxLmVuLnNydA=="}
	if !reflect.DeepEqual(deleted, want) {
		t.Errorf("deleted = %v, want %v (the other film untouched)", deleted, want)
	}
}

func TestDownloader_RemoveCancelsEveryTransferOfTheFolderAndDeletesIt(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/transfers/downloads": {Body: `[{"username":"peer","directories":[{"directory":"@@music\\A\\Done","files":[{"id":"1"},{"id":"2"}]},
		 {"directory":"@@music\\A\\Other","files":[{"id":"3"}]}]}]`},
		"DELETE /api/v0/transfers/downloads/peer/1": {}, "DELETE /api/v0/transfers/downloads/peer/2": {},
		"DELETE /api/v0/files/downloads/directories/RG9uZQ==": {},
	}}
	if err := driver(t, server).Remove(t.Context(), `peer|@@music\A\Done`, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var deleted []string
	for _, seen := range server.Seen {
		if seen.Method == http.MethodDelete {
			deleted = append(deleted, seen.Path)
		}
	}
	want := []string{"/api/v0/transfers/downloads/peer/1", "/api/v0/transfers/downloads/peer/2", "/api/v0/files/downloads/directories/RG9uZQ=="}
	if !reflect.DeepEqual(deleted, want) {
		t.Errorf("deleted = %v, want %v", deleted, want)
	}
}

func TestDownloader_PauseIsNotSupported(t *testing.T) {
	if err := driver(t, &clienttest.Server{}).Pause(t.Context(), []string{"peer|x"}); !errors.Is(err, download.ErrNotSupported) {
		t.Errorf("Pause = %v, want ErrNotSupported", err)
	}
}

func TestDownloader_ProbeReadsTheVersionAndDownloadsFolder(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/application": {Body: `{"version":{"full":"0.21.4.65534+abc","current":"0.21.4"}}`},
		"/api/v0/options":     {Body: `{"directories":{"downloads":"/app/downloads"}}`},
	}}
	got, err := driver(t, server).Probe(t.Context())
	if err != nil || got.Version != "slskd 0.21.4" || got.DownloadFolder != "/app/downloads" {
		t.Errorf("Probe = %+v, %v", got, err)
	}
}

// httpClient is an httpx.Client over server, with limits no test reaches.
func httpClient(t *testing.T, server *clienttest.Server) *httpx.Client {
	t.Helper()
	fake := clock.NewFakeAtEpoch()
	limiter := httpx.NewLimiter(fake, httpx.Config{Defaults: httpx.Limits{RequestsPerMinute: 6000}}, httpx.NoJitter)
	breaker := httpx.NewBreaker(fake, httpx.BreakerConfig{FailureThreshold: 100, Cooldown: time.Minute})
	return httpx.NewClient(fake, httpx.NewGuard(nil, nil, nil), limiter, breaker, httpx.ClientConfig{
		Provider: "download-client-test", UserAgent: "test", Transport: server,
	})
}
