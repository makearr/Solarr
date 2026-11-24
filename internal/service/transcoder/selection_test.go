package transcoder_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

// failedFiles puts ids 1..n into the pipeline, odd ones failed and even ones blocked, plus a done one.
func failedFiles(t *testing.T) *pipeline {
	t.Helper()
	p := newPipeline(t, musicTarget(), nil)
	for id := int64(1); id <= 4; id++ {
		state := library.TranscodeFailed
		if id%2 == 0 {
			state = library.TranscodeBlocked
		}
		p.items.items[id] = &library.TranscodeItem{MediaObjectID: id, CategoryID: 3, Kind: "audio", State: state, Attempts: 3, FailureCode: "error", LastError: "boom"}
	}
	p.items.items[5] = &library.TranscodeItem{MediaObjectID: 5, CategoryID: 3, Kind: "audio", State: library.TranscodeDone}
	return p
}

func TestRetry_PreviewCountsWhatApplyingTouchesAndLeavesEveryoneAlone(t *testing.T) {
	p := failedFiles(t)
	selection := transcoder.Selection{CategoryID: 3, Excluded: []int64{2}}
	preview, err := p.svc.Retry(context.Background(), selection, transcoder.Override{}, false)
	if err != nil || preview != 3 || p.item(1).State != library.TranscodeFailed {
		t.Fatalf("preview = %d, %v, item 1 %+v; want 3 and nothing changed", preview, err, p.item(1))
	}
	applied, err := p.svc.Retry(context.Background(), selection, transcoder.Override{Quality: 24, Preset: "slow"}, true)
	if err != nil || applied != preview {
		t.Fatalf("applied = %d, %v; want the previewed %d", applied, err, preview)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded || item.Attempts != 0 || item.FailureCode != "" || item.OverrideJSON != `{"quality":24,"preset":"slow"}` {
		t.Errorf("item 1 = %+v, want needed with the override and a clean slate", item)
	}
	if p.item(2).State != library.TranscodeBlocked || p.item(5).State != library.TranscodeDone {
		t.Errorf("the excluded file and the done file must stay: %+v %+v", p.item(2), p.item(5))
	}
}

func TestRetry_ByIdsTouchesOnlyFailedAndBlockedAndAnInvalidOverrideChangesNothing(t *testing.T) {
	p := failedFiles(t)
	touched, err := p.svc.Retry(context.Background(), transcoder.Selection{IDs: []int64{1, 5, 99}}, transcoder.Override{}, true)
	if err != nil || touched != 1 || p.item(5).State != library.TranscodeDone {
		t.Errorf("touched %d, %v; want only the failed file", touched, err)
	}
	_, err = p.svc.Retry(context.Background(), transcoder.Selection{IDs: []int64{3}}, transcoder.Override{Engine: "tpu"}, true)
	if !errors.Is(err, transcoder.ErrBadOverride) || p.item(3).State != library.TranscodeFailed {
		t.Errorf("err = %v, item 3 %+v; want a refusal that changed nothing", err, p.item(3))
	}
}

func TestResumeHealth_EndsBackoff(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	_ = p.items.SaveHealth(context.Background(), library.TranscodeHealth{Kind: "video", Mode: "backoff", BackoffUntil: 5000, ConsecutiveFailures: 3, RunSeq: 7})
	if err := p.svc.ResumeHealth(context.Background(), "video"); err != nil {
		t.Fatal(err)
	}
	got, _ := p.items.Health(context.Background(), "video")
	if got.Mode != "ok" || got.BackoffUntil != 0 || got.ConsecutiveFailures != 0 || got.RunSeq != 7 {
		t.Errorf("health = %+v, want ok, no backoff and the run sequence kept", got)
	}
}
