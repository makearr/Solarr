package sabnzbd_test

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
)

// modeTransport answers each SABnzbd mode with its own body.
type modeTransport map[string]string

func (m modeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(m[request.URL.Query().Get("mode")])), Request: request,
	}, nil
}

// The queue and the history become one list in Solarr's states; only a completed download has a path.
func TestDownloader_MapsQueueAndHistory(t *testing.T) {
	client := sabnzbd.NewClient(newTestClient(t, modeTransport{"queue": queueFixtureJSON, "history": historyFixtureJSON}),
		"http://sabnzbd.local:8080", "key")

	downloads, err := sabnzbd.Downloader{Client: client}.Downloads(t.Context(), "music")
	if err != nil {
		t.Fatalf("Downloads: %v", err)
	}
	var got []string
	for _, entry := range downloads {
		got = append(got, entry.ClientRef+" "+string(entry.State)+" "+entry.Path)
	}
	want := []string{
		"SABnzbd_nzo_abc123 downloading ",
		"SABnzbd_nzo_def456 queued ",
		"SABnzbd_nzo_abc123 completed /downloads/complete/Heka-Watermark-1988-FLAC",
		"SABnzbd_nzo_def456 failed ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("downloads = %q, want %q", got, want)
	}
	if downloads[3].Error != "Repair failed, not enough repair blocks" || downloads[3].State != download.StateFailed {
		t.Errorf("failed download = %+v, want its reason", downloads[3])
	}
}
