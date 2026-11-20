package slskd_test

import (
	"errors"
	"net/http"
	"reflect"
	"strings"
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
	if body := server.Seen[len(server.Seen)-1].Body; body != `[{"filename":"@@music\\Heka\\Zanuvode\\01.flac","size":10},{"filename":"@@music\\Heka\\Zanuvode\\02.flac","size":20}]` {
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

func TestDownloader_RemoveCancelsEveryTransferOfTheFolderAndDeletesItsFilesOnly(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/transfers/downloads": {Body: `[{"username":"peer","directories":[{"directory":"@@music\\A\\Done","files":[{"id":"1","filename":"@@music\\A\\Done\\01.flac"},{"id":"2","filename":"@@music\\A\\Done\\02.flac"}]},
		 {"directory":"@@music\\A\\Other","files":[{"id":"3"}]}]}]`},
		"DELETE /api/v0/transfers/downloads/peer/1": {}, "DELETE /api/v0/transfers/downloads/peer/2": {},
		"DELETE /api/v0/files/downloads/files/RG9uZS8wMS5mbGFj": {}, "DELETE /api/v0/files/downloads/files/RG9uZS8wMi5mbGFj": {},
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
	want := []string{"/api/v0/transfers/downloads/peer/1", "/api/v0/transfers/downloads/peer/2",
		"/api/v0/files/downloads/files/RG9uZS8wMS5mbGFj", "/api/v0/files/downloads/files/RG9uZS8wMi5mbGFj"}
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

// Two peers' folders of one name land in one place, so the second is not sent.
func TestDownloader_AFolderNamedLikeOneAlreadyHeldIsRefused(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"POST /api/v0/transfers/downloads/peer two": {Status: http.StatusCreated},
		"/api/v0/transfers/downloads":               {Body: `[{"username":"peer one","directories":[{"directory":"@@music\\Other\\Zanuvode","files":[{"id":"1","filename":"@@music\\Other\\Zanuvode\\01.flac"}]}]}]`},
	}}
	second := `{"source":"peer two","whole_folder":true,"files":[{"path":"@@music\\Heka\\Zanuvode\\01.flac","size":10}]}`
	if _, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(second)}); !errors.Is(err, download.ErrRefused) {
		t.Errorf("Add = %v, want ErrRefused", err)
	}
	for _, seen := range server.Seen {
		if seen.Method == http.MethodPost {
			t.Errorf("slskd was asked for the files: %s", seen.Path)
		}
	}
	// The same peer's same folder asked again is the same download, not a clash.
	same := `{"source":"peer one","whole_folder":true,"files":[{"path":"@@music\\Other\\Zanuvode\\01.flac","size":10}]}`
	server.Routes["POST /api/v0/transfers/downloads/peer one"] = clienttest.Route{Status: http.StatusCreated}
	if _, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(same)}); err != nil {
		t.Errorf("the same folder again: %v", err)
	}
}

func TestDownloader_OwnFilesAreTheNamesOfItsTransfers(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/transfers/downloads": {Body: `[{"username":"peer","directories":[{"directory":"@@music\\A\\Done","files":[
		 {"id":"1","filename":"@@music\\A\\Done\\01.flac"},{"id":"2","filename":"@@music\\A\\Done\\02.flac"}]},
		 {"directory":"@@music\\B\\Done","files":[{"id":"3","filename":"@@music\\B\\Done\\09.flac"}]}]}]`},
	}}
	own, ok := driver(t, server).(download.OwnFiles)
	if !ok {
		t.Fatal("the direct driver does not forward OwnFiles")
	}
	got, err := own.OwnFiles(t.Context(), `peer|@@music\A\Done`)
	if want := []string{"01.flac", "02.flac"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("OwnFiles = %v, %v, want %v", got, err, want)
	}
}

// Half an album the peer refused is not tracked as a whole one: what was taken is cancelled and the release refused.
func TestDownloader_APartlyRefusedEnqueueIsRefusedAndCancelled(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"POST /api/v0/transfers/downloads/peer one":     {Status: http.StatusCreated, Body: `{"enqueued":[{"id":"7","filename":"@@music\\Heka\\Zanuvode\\01.flac"}],"failed":["@@music\\Heka\\Zanuvode\\02.flac: not shared"]}`},
		"DELETE /api/v0/transfers/downloads/peer one/7": {},
	}}
	_, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(release)})
	if !errors.Is(err, download.ErrRefused) {
		t.Fatalf("Add = %v, want ErrRefused", err)
	}
	cancelled := false
	for _, seen := range server.Seen {
		cancelled = cancelled || (seen.Method == http.MethodDelete && seen.Path == "/api/v0/transfers/downloads/peer one/7")
	}
	if !cancelled {
		t.Errorf("the accepted transfer was not cancelled: %+v", server.Seen)
	}
}

// slskd ships remote file management off and answers 403: the download still leaves the queue, its files stay.
func TestDownloader_RemoveWithoutRemoteFileManagementLeavesTheFilesAndSucceeds(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"/api/v0/transfers/downloads":                           {Body: `[{"username":"peer","directories":[{"directory":"@@music\\A\\Done","files":[{"id":"1","filename":"@@music\\A\\Done\\01.flac"}]}]}]`},
		"DELETE /api/v0/transfers/downloads/peer/1":             {},
		"DELETE /api/v0/files/downloads/files/RG9uZS8wMS5mbGFj": {Status: http.StatusForbidden},
	}}
	if err := driver(t, server).Remove(t.Context(), `peer|@@music\A\Done`, true); err != nil {
		t.Errorf("Remove = %v, want nil", err)
	}
}

const twoDiscs = `{"source":"peer","whole_folder":true,"files":[{"path":"@@music\\Heka\\Zanuvode\\CD1\\01.flac","size":10},{"path":"@@music\\Heka\\Zanuvode\\CD2\\01.flac","size":20}]}`

const twoDiscTransfers = `[{"username":"peer","directories":[
 {"directory":"@@music\\Heka\\Zanuvode\\CD1","files":[{"id":"1","filename":"@@music\\Heka\\Zanuvode\\CD1\\01.flac","state":"Completed, Succeeded","size":10}]},
 {"directory":"@@music\\Heka\\Zanuvode\\CD2","files":[{"id":"2","filename":"@@music\\Heka\\Zanuvode\\CD2\\01.flac","state":"Completed, Succeeded","size":20}]},
 {"directory":"@@music\\Heka\\Other\\Disc 1","files":[{"id":"3","filename":"@@music\\Heka\\Other\\Disc 1\\01.flac","state":"Queued, Remotely","size":5}]}]}]`

func TestDownloader_AMultiDiscAlbumIsOneDownloadGatheredInOneFolder(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{
		"POST /api/v0/transfers/downloads/peer": {Status: http.StatusCreated},
		"/api/v0/options":                       {Body: `{"directories":{"downloads":"/app/downloads"}}`},
		"/api/v0/transfers/downloads":           {Body: twoDiscTransfers},
	}}
	ref, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(twoDiscs)})
	if want := `peer|@@music\Heka\Zanuvode\*`; err != nil || ref != want {
		t.Fatalf("Add = %q, %v, want %q", ref, err, want)
	}
	got, err := driver(t, server).Downloads(t.Context(), "")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var album download.Download
	for _, one := range got {
		if one.ClientRef == ref {
			album = one
		}
	}
	if album.State != download.StateCompleted || album.SizeBytes != 30 || album.Name != "Zanuvode" || !strings.HasPrefix(album.Path, "/app/downloads/.solarr/d-") {
		t.Errorf("album = %+v, want both discs as one finished download below .solarr", album)
	}
	target, sources, err := driver(t, server).(download.Gatherer).Gather(t.Context(), ref)
	if wantSources := []string{"/app/downloads/CD1", "/app/downloads/CD2"}; err != nil || target != album.Path || !reflect.DeepEqual(sources, wantSources) {
		t.Errorf("Gather = %q, %v, %v; want %q and %v", target, sources, err, album.Path, wantSources)
	}
	if names, err := driver(t, server).(download.OwnFiles).OwnFiles(t.Context(), ref); names != nil || err != nil {
		t.Errorf("OwnFiles = %v, %v; the gathered folder is all this download's", names, err)
	}
}

func TestDownloader_FilesInFoldersThatAreNotDiscsOfOneAlbumAreRefused(t *testing.T) {
	server := &clienttest.Server{}
	other := `{"source":"peer","whole_folder":true,"files":[{"path":"@@music\\A\\CD1\\01.flac","size":1},{"path":"@@music\\B\\CD2\\01.flac","size":1}]}`
	if _, err := driver(t, server).Add(t.Context(), download.AddRequest{File: []byte(other)}); !errors.Is(err, slskd.ErrSpansFolders) {
		t.Errorf("two albums: %v, want ErrSpansFolders", err)
	}
}

func TestDownloader_RemovingAMultiDiscAlbumDeletesTheGatheredFolderToo(t *testing.T) {
	server := &clienttest.Server{Routes: map[string]clienttest.Route{"/api/v0/transfers/downloads": {Body: twoDiscTransfers},
		"DELETE /api/v0/transfers/downloads/peer/1": {}, "DELETE /api/v0/transfers/downloads/peer/2": {}}}
	if err := driver(t, server).Remove(t.Context(), `peer|@@music\Heka\Zanuvode\*`, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	var cancelled []string
	for _, seen := range server.Seen {
		if seen.Method == http.MethodDelete && strings.Contains(seen.Path, "/transfers/") {
			cancelled = append(cancelled, seen.Path)
		}
	}
	if want := []string{"/api/v0/transfers/downloads/peer/1", "/api/v0/transfers/downloads/peer/2"}; !reflect.DeepEqual(cancelled, want) {
		t.Errorf("cancelled = %v, want %v (the other album's disc stays)", cancelled, want)
	}
	if last := server.Seen[len(server.Seen)-1]; last.Method != http.MethodDelete || !strings.Contains(last.Path, "/files/downloads/directories/") {
		t.Errorf("last request = %s %s, want the gathered folder deleted", last.Method, last.Path)
	}
}
