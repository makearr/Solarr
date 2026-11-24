package transcoder_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
)

func needed(p *pipeline, id int64, rel string, interrupted bool) {
	p.items.items[id] = &library.TranscodeItem{MediaObjectID: id, CategoryID: 3, Kind: "audio", State: library.TranscodeNeeded, Lane: "audio", Interrupted: interrupted}
	p.items.paths[id] = []library.ObjectPath{{RootFolderID: 7, RelPath: rel}}
}

func fileJobs(q *queuedJobs) []string {
	var keys []string
	for _, request := range q.requests {
		if request.Type == "transcode.file" {
			keys = append(keys, request.ActionKey)
		}
	}
	return keys
}

func TestDispatch_StartsTheWaitingFilesInOrderAndQueuesTheAnalysis(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	needed(p, 1, "a/1.flac", false)
	needed(p, 2, "b/2.flac", true)
	needed(p, 3, "c/3.flac", false)
	if err := p.svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := fileJobs(p.queue)
	want := []string{"transcode:2", "transcode:1", "transcode:3"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("file jobs = %v, want the interrupted file first, then by path: %v", got, want)
	}
	for id := int64(1); id <= 3; id++ {
		if item := p.item(id); item.State != library.TranscodeRunning || item.LastRunSeq == 0 {
			t.Errorf("item %d = %+v, want running with a run number", id, item)
		}
	}
	analyses := 0
	for _, request := range p.queue.requests {
		if request.Type == "transcode.analyze" && request.ActionKey == "transcode.analyze:"+strconv.Itoa(3) {
			analyses++
		}
	}
	if analyses != 1 {
		t.Errorf("analyses queued = %d, want 1", analyses)
	}
}

func TestDispatch_AStartedFileShowsNoErrorOfItsEarlierRun(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	needed(p, 1, "a/1.flac", false)
	p.items.items[1].LastError, p.items.items[1].FailureCode = "result_above_range", "result_above_range"
	if err := p.svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeRunning || item.LastError != "" || item.FailureCode != "" {
		t.Errorf("item = %+v, want running with the old error cleared", item)
	}
}

func TestDispatch_TheMasterSwitchOffQueuesNothing(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	needed(p, 1, "a/1.flac", false)
	p.settings.global.Enabled = false
	if err := p.svc.Dispatch(context.Background()); err != nil || len(p.queue.requests) != 0 || p.item(1).State != library.TranscodeNeeded {
		t.Errorf("err %v, requests %d, state %s; want nothing started", err, len(p.queue.requests), p.item(1).State)
	}
}

func TestDispatch_AKindInBackoffWaitsAndThenProbesWithOneFileFarAway(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	needed(p, 1, "a/1.flac", false)
	needed(p, 2, "z/2.flac", false)
	needed(p, 3, "m/3.flac", false)
	until := p.clock.Now().Add(time.Hour)
	_ = p.items.SaveHealth(context.Background(), library.TranscodeHealth{Kind: "audio", Mode: "backoff", BackoffUntil: until.Unix(), LastFailedID: 1})
	if err := p.svc.Dispatch(context.Background()); err != nil || len(fileJobs(p.queue)) != 0 {
		t.Fatalf("err %v, file jobs %v; want none during the backoff", err, fileJobs(p.queue))
	}
	p.clock.Advance(2 * time.Hour)
	if err := p.svc.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := fileJobs(p.queue); len(got) != 1 || got[0] != "transcode:2" {
		t.Errorf("file jobs = %v, want one probe from the far end, away from the failed file's folder", got)
	}
}
