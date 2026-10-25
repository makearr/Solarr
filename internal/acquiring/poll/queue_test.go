package poll_test

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// slowClient takes a while to answer and counts how often it was asked.
type slowClient struct {
	download.Client
	delay time.Duration
	asked atomic.Int32
}

func (c *slowClient) QueueStatus(context.Context) (download.QueueStatus, error) {
	c.asked.Add(1)
	time.Sleep(c.delay)
	return download.QueueStatus{Items: 1}, nil
}

func (c *slowClient) Downloads(context.Context, string) ([]download.Download, error) { return nil, nil }

// Pages polling together share one round of client reads, and the clients are asked at once, not one after another.
func TestQueue_SharesOneReadOfTheClientsAndAsksThemTogether(t *testing.T) {
	e := newEnv(t)
	first, second := &slowClient{delay: 200 * time.Millisecond}, &slowClient{delay: 200 * time.Millisecond}
	job := poll.NewJob(poll.Config{
		Acquisitions: e.acquisitions, Clock: e.clock,
		Clients: func(context.Context) (map[int64]poll.Client, error) {
			return map[int64]poll.Client{1: {Name: "SABnzbd", Client: first}, 2: {Name: "qBittorrent", Client: second}}, nil
		},
	})

	started := time.Now()
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			if _, clients, err := job.Queue(t.Context()); err != nil || len(clients) != 2 {
				t.Errorf("Queue = %d clients, %v; want 2", len(clients), err)
			}
		})
	}
	wg.Wait()
	if elapsed := time.Since(started); elapsed >= 390*time.Millisecond {
		t.Errorf("five reads of two clients took %v; the clients were asked one after another", elapsed)
	}
	if first.asked.Load() != 1 || second.asked.Load() != 1 {
		t.Errorf("clients asked %d and %d times, want once each", first.asked.Load(), second.asked.Load())
	}

	// Once the read is old enough, the next one asks again.
	e.clock.Advance(3 * time.Second)
	if _, _, err := job.Queue(t.Context()); err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if first.asked.Load() != 2 {
		t.Errorf("after the read went stale the client was asked %d times, want 2", first.asked.Load())
	}
}
