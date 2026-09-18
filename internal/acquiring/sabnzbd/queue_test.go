package sabnzbd_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
)

// queueFixtureJSON is a representative mode=queue response, trimmed to the
// fields this package reads -- SABnzbd's own size/speed fields are decimal
// strings in MB and KB/s, not raw byte counts, which is the real-world
// quirk this fixture exists to exercise.
const queueFixtureJSON = `{
  "queue": {
    "paused": false,
    "mb": "12583.5",
    "mbleft": "8294.4",
    "kbpersec": "4800.00",
    "noofslots_total": 2,
    "slots": [
      {
        "nzo_id": "SABnzbd_nzo_abc123",
        "filename": "Artist.Name-Album.Title-2020-FLAC-GROUP",
        "cat": "music",
        "mb": "734.4",
        "mbleft": "200.1",
        "percentage": "72",
        "status": "Downloading",
        "timeleft": "0:05:23"
      },
      {
        "nzo_id": "SABnzbd_nzo_def456",
        "filename": "Other.Release",
        "cat": "music",
        "mb": "500.0",
        "mbleft": "500.0",
        "percentage": "0",
        "status": "Queued",
        "timeleft": "0:12:00"
      }
    ]
  }
}`

func TestQueue_ConvertsMegabytesAndKilobytesToBytes(t *testing.T) {
	transport := &recordingTransport{body: queueFixtureJSON}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "test-api-key")

	state, err := client.Queue(context.Background())
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}

	if transport.requested[0].URL.Query().Get("mode") != "queue" {
		t.Errorf("mode = %q, want queue", transport.requested[0].URL.Query().Get("mode"))
	}

	wantSizeBytes := mb(12583.5)
	if state.SizeBytes != wantSizeBytes {
		t.Errorf("SizeBytes = %d, want %d (12583.5 MB)", state.SizeBytes, wantSizeBytes)
	}
	wantSizeLeftBytes := mb(8294.4)
	if state.SizeLeftBytes != wantSizeLeftBytes {
		t.Errorf("SizeLeftBytes = %d, want %d (8294.4 MB)", state.SizeLeftBytes, wantSizeLeftBytes)
	}
	wantSpeed := kb(4800.00)
	if state.SpeedBytesPerSec != wantSpeed {
		t.Errorf("SpeedBytesPerSec = %d, want %d (4800.00 KB/s)", state.SpeedBytesPerSec, wantSpeed)
	}
	if state.Paused {
		t.Error("Paused = true, want false")
	}
	if state.SlotsTotal != 2 {
		t.Errorf("SlotsTotal = %d, want 2", state.SlotsTotal)
	}
	if len(state.Slots) != 2 {
		t.Fatalf("len(Slots) = %d, want 2", len(state.Slots))
	}

	first := state.Slots[0]
	if first.ClientRef != "SABnzbd_nzo_abc123" {
		t.Errorf("ClientRef = %q, want the nzo_id", first.ClientRef)
	}
	if first.Category != "music" || first.PercentComplete != 72 || first.Status != "Downloading" {
		t.Errorf("first = %+v, want Category=music PercentComplete=72 Status=Downloading", first)
	}
	wantFirstSize := mb(734.4)
	if first.SizeBytes != wantFirstSize {
		t.Errorf("first.SizeBytes = %d, want %d", first.SizeBytes, wantFirstSize)
	}
}

// mb/kb mirror the package's own unit conversion, computed at runtime
// rather than as untyped float constants -- 8294.4 * 1024 * 1024 as a
// compile-time constant expression does not convert cleanly to int64.
func mb(value float64) int64 { return int64(value * 1024 * 1024) }
func kb(value float64) int64 { return int64(value * 1024) }

func TestQueue_UnparseableSizeAnswersZeroNotAnError(t *testing.T) {
	transport := &recordingTransport{body: `{"queue": {"mb": "not a number", "slots": []}}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "")

	state, err := client.Queue(context.Background())
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if state.SizeBytes != 0 {
		t.Errorf("SizeBytes = %d, want 0 for an unparseable field", state.SizeBytes)
	}
}
