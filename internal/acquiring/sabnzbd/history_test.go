package sabnzbd_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
)

const historyFixtureJSON = `{"history": {"slots": [
  {"nzo_id": "SABnzbd_nzo_abc123", "name": "Heka-Zanuvode-1988-FLAC", "category": "music",
   "status": "Completed", "storage": "/downloads/complete/Heka-Zanuvode-1988-FLAC",
   "fail_message": "", "bytes": 300000000, "completed": 1790000000},
  {"nzo_id": "SABnzbd_nzo_def456", "name": "Broken", "category": "music", "status": "Failed",
   "storage": "", "fail_message": "Repair failed, not enough repair blocks", "bytes": 0, "completed": 1790000100}
]}}`

func TestHistory_ReadsCompletedAndFailedDownloads(t *testing.T) {
	transport := &recordingTransport{body: historyFixtureJSON}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "key")

	slots, err := client.History(t.Context(), "music", 50)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	query := transport.requested[0].URL.Query()
	if query.Get("mode") != "history" || query.Get("category") != "music" || query.Get("limit") != "50" {
		t.Errorf("query = %v, want mode=history category=music limit=50", query)
	}
	want := []sabnzbd.HistorySlot{
		{ClientRef: "SABnzbd_nzo_abc123", Name: "Heka-Zanuvode-1988-FLAC", Category: "music", Status: "Completed",
			StoragePath: "/downloads/complete/Heka-Zanuvode-1988-FLAC", SizeBytes: 300000000, CompletedAt: time.Unix(1790000000, 0)},
		{ClientRef: "SABnzbd_nzo_def456", Name: "Broken", Category: "music", Status: "Failed",
			FailMessage: "Repair failed, not enough repair blocks", CompletedAt: time.Unix(1790000100, 0)},
	}
	if !reflect.DeepEqual(slots, want) {
		t.Errorf("slots = %+v, want %+v", slots, want)
	}
}

func TestRemove_DeletesFromQueueAndHistoryWithFiles(t *testing.T) {
	transport := &recordingTransport{body: `{"status": true}`}
	client := sabnzbd.NewClient(newTestClient(t, transport), "http://sabnzbd.local:8080", "key")

	if err := client.Remove(t.Context(), "SABnzbd_nzo_abc123", true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(transport.requested) != 2 {
		t.Fatalf("%d requests, want queue and history", len(transport.requested))
	}
	for index, mode := range []string{"queue", "history"} {
		query := transport.requested[index].URL.Query()
		if query.Get("mode") != mode || query.Get("name") != "delete" || query.Get("value") != "SABnzbd_nzo_abc123" ||
			query.Get("del_files") != "1" {
			t.Errorf("request %d = %v, want %s delete with files", index, query, mode)
		}
	}
}
