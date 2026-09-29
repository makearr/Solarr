package poll_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/poll"
)

// The queue joins Solarr's record to the client's live view, and lists the client itself, paused
// or not, so a grab sitting at QUEUED says why (found 2026-09-25).
func TestQueue_JoinsEachDownloadToWhatTheClientReports(t *testing.T) {
	e := newEnv(t)
	downloading := e.send(t, "nzo-1")
	notYetListed := e.send(t, "nzo-2")
	e.client.status = download.QueueStatus{Paused: true, SpeedBytesPerSec: 0, Items: 1, SizeLeftBytes: 600}
	reported := download.Download{
		ClientRef: "nzo-1", Name: "Heka-Zanuvode", State: download.StateDownloading, SizeBytes: 1000, SizeLeftBytes: 600,
	}
	e.client.downloads = []download.Download{reported}

	entries, clients, err := e.job().Queue(t.Context())
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}

	want := []poll.Entry{
		{Acquisition: downloading, ClientName: "Home SABnzbd", Reported: true, Download: reported},
		{Acquisition: notYetListed, ClientName: "Home SABnzbd"},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries =\n%+v\nwant\n%+v", entries, want)
	}
	wantClients := []poll.ClientStatus{{ID: 1, Name: "Home SABnzbd", Status: e.client.status}}
	if !reflect.DeepEqual(clients, wantClients) {
		t.Errorf("clients = %+v, want %+v", clients, wantClients)
	}
}
