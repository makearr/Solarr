package transcoder_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

const trackSeconds = 200

func flacTrack() transcode.File {
	return transcode.File{
		Container: "flac", Duration: trackSeconds * time.Second, BitrateBps: 900_000,
		Streams: []transcode.Stream{{Index: 0, Type: transcode.StreamAudio, Codec: "flac", Channels: 2, SampleRate: 44100, BitrateBps: 900_000}},
	}
}

func m4aResult(size int) transcode.File {
	return transcode.File{
		Container: "mov,mp4,m4a,3gp,3g2,mj2", Duration: trackSeconds * time.Second, SizeBytes: int64(size), BitrateBps: int64(size) * 8 / trackSeconds,
		Streams: []transcode.Stream{{Index: 0, Type: transcode.StreamAudio, Codec: "aac", Channels: 2, SampleRate: 48000, BitrateBps: 128_000}},
	}
}

func musicPipeline(t *testing.T) *pipeline {
	t.Helper()
	p := newPipeline(t, musicTarget(), nil)
	p.add(t, 1, "Album/t1.flac", "audio", flacTrack(), 3_000_000)
	return p
}

func TestRunFile_ConvertsAFlacToTheTargetAndReplacesTheOriginalThroughTheBin(t *testing.T) {
	p := musicPipeline(t)
	p.outputs(t, []int{800_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeDone || item.OriginalRecycleID != 1 || item.ResultSizeBytes != 800_000 || item.Engine != "software" {
		t.Errorf("item = %+v, want done with the original in bin entry 1 and the result's size", item)
	}
	if p.exists("/lib/Album/t1.flac") || !p.exists("/lib/Album/t1.m4a") || !p.exists("/lib/recycle-bin/day/1-t1.flac") {
		t.Errorf("flac present %v, m4a present %v, bin copy present %v; want only the m4a in place and the flac in the bin", p.exists("/lib/Album/t1.flac"), p.exists("/lib/Album/t1.m4a"), p.exists("/lib/recycle-bin/day/1-t1.flac"))
	}
	if len(p.items.replaced) != 1 || p.items.replaced[0].NewRelPath != "Album/t1.m4a" || p.items.replaced[0].SizeBytes != 800_000 {
		t.Errorf("replaced = %+v, want the new path and size handed to the library", p.items.replaced)
	}
	if len(p.reported.done) != 1 || p.reported.done[0].OriginalBytes != 3_000_000 || p.reported.done[0].Passes != 1 {
		t.Errorf("reported = %+v, want one conversion of 3 MB in one pass", p.reported.done)
	}
	if p.exists("/lib/recycle-bin/.transcode/1.0.m4a") {
		t.Error("the work file was left behind")
	}
	if got := strings.Join(p.items.stages, ","); got != "recycling,placing," {
		t.Errorf("journal stages = %q, want recycling, placing, then cleared", got)
	}
	if item.ReplaceStage != "" || item.WorkPath != "" {
		t.Errorf("journal = %q %q, want it cleared", item.ReplaceStage, item.WorkPath)
	}
}

func TestRunFile_ALargerResultGoesDownTheLadderAndTheSecondPassIsAccepted(t *testing.T) {
	p := musicPipeline(t)
	p.outputs(t, []int{4_000_000, 800_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if got := len(p.starter.Started()); got != 2 {
		t.Errorf("ffmpeg started %d times, want 2 passes", got)
	}
	if item := p.item(1); item.State != library.TranscodeDone || item.Pass != 1 {
		t.Errorf("item = %+v, want done on pass 1", item)
	}
	if len(p.reported.done) != 1 || p.reported.done[0].Passes != 2 {
		t.Errorf("reported = %+v, want two passes", p.reported.done)
	}
}

func TestRunFile_AResultThatStaysLargerFailsOnceTheLadderIsUsedUp(t *testing.T) {
	p := musicPipeline(t)
	p.outputs(t, []int{4_000_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	item := p.item(1)
	if item.State != library.TranscodeFailed || item.FailureCode != "result_larger" {
		t.Errorf("item = %+v, want failed with result_larger", item)
	}
	if !p.exists("/lib/Album/t1.flac") || p.exists("/lib/Album/t1.m4a") || p.exists("/lib/recycle-bin/.transcode/1.2.m4a") {
		t.Error("a refused conversion must leave the original alone and no result behind")
	}
	if got := len(p.starter.Started()); got != 3 {
		t.Errorf("ffmpeg started %d times, want the first pass and two extra", got)
	}
}

func TestRunFile_AnErrorCountsAnAttemptAndTheThirdOneFailsTheFile(t *testing.T) {
	p := musicPipeline(t)
	p.starter.Next = func([]string) transcode.FakeRun {
		return transcode.FakeRun{Err: &transcode.RunError{ExitCode: 1, LastLine: "boom"}, Reports: []transcode.Progress{{OutTime: 100 * time.Second}}}
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := p.run(t, 1); err != nil {
			t.Fatal(err)
		}
		item := p.item(1)
		wantState := library.TranscodeNeeded
		if attempt == 3 {
			wantState = library.TranscodeFailed
		}
		if item.State != wantState || item.Attempts != int64(attempt) {
			t.Fatalf("attempt %d: item = %+v, want %s with %d attempts", attempt, item, wantState, attempt)
		}
		if attempt < 3 {
			_ = p.items.Transition(context.Background(), 1, library.TranscodeNeeded, library.TranscodeRunning, nil)
		}
	}
	if item := p.item(1); item.FailureCode != "error" || item.LastError == "" {
		t.Errorf("item = %+v, want the failure code error and the last line kept", item)
	}
}

func TestRunFile_ACancelledRunLeavesTheItemFirstInLineWithItsAttemptsUntouched(t *testing.T) {
	p := musicPipeline(t)
	block := make(chan struct{})
	p.starter.Next = func(args []string) transcode.FakeRun {
		p.writeFile(t, args[len(args)-1], 10)
		return transcode.FakeRun{Block: block}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.svc.RunFile(ctx, 1, nil) }()
	for len(p.starter.Started()) == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunFile = %v, want the cancellation", err)
	}
	item := p.item(1)
	if item.State != library.TranscodeNeeded || !item.Interrupted || item.Attempts != 0 {
		t.Errorf("item = %+v, want needed, interrupted and no attempt spent", item)
	}
	if p.exists("/lib/recycle-bin/.transcode/1.0.m4a") || !p.exists("/lib/Album/t1.flac") {
		t.Error("an interrupted conversion must delete its output and keep the original")
	}
}

func TestRunFile_ASourceChangedDuringTheEncodeIsNotReplaced(t *testing.T) {
	p := musicPipeline(t)
	p.outputs(t, []int{800_000}, m4aResult)
	inner := p.starter.Next
	p.starter.Next = func(args []string) transcode.FakeRun {
		p.writeFile(t, "/lib/Album/t1.flac", 3_000_001)
		return inner(args)
	}
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded || item.Attempts != 0 {
		t.Errorf("item = %+v, want needed again with no attempt spent", item)
	}
	if len(p.bin.entries) != 0 || p.exists("/lib/recycle-bin/.transcode/1.0.m4a") {
		t.Error("nothing may be recycled or left over when the source changed")
	}
}

func TestRunFile_APathTheResultWouldTakeFailsTheFileBeforeAnythingMoves(t *testing.T) {
	p := musicPipeline(t)
	p.writeFile(t, "/lib/Album/t1.m4a", 5)
	p.outputs(t, []int{800_000}, m4aResult)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeFailed || item.FailureCode != "path_taken" {
		t.Errorf("item = %+v, want failed with path_taken", item)
	}
	if !p.exists("/lib/Album/t1.flac") || len(p.bin.entries) != 0 {
		t.Error("the original must stay and nothing go to the bin")
	}
}

func TestRunFile_ADiskShortOfSpaceIsNotStartedAndHoldsItsRoot(t *testing.T) {
	p := musicPipeline(t)
	p.fs.SetDiskUsage(1<<30, 1_000_000)
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded {
		t.Errorf("item = %+v, want needed again", item)
	}
	if len(p.starter.Started()) != 0 {
		t.Error("ffmpeg must not start on a full disk")
	}
}

func TestRunFile_TheMasterSwitchGoingOffReleasesTheItem(t *testing.T) {
	p := musicPipeline(t)
	p.settings.global.Enabled = false
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded || len(p.starter.Started()) != 0 {
		t.Errorf("item = %+v, started %d, want needed and nothing started", item, len(p.starter.Started()))
	}
}

func TestRunFile_AFileAlreadyInTargetShapeIsNotNeededAndTwoPathsAreBlocked(t *testing.T) {
	p := newPipeline(t, musicTarget(), nil)
	p.add(t, 1, "Album/t1.m4a", "audio", m4aResult(3_000_000), 3_000_000)
	p.add(t, 2, "Album/t2.flac", "audio", flacTrack(), 3_000_000)
	p.items.paths[2] = append(p.items.paths[2], library.ObjectPath{RootFolderID: 7, RelPath: "Other/t2.flac"})
	for _, id := range []int64{1, 2} {
		if err := p.run(t, id); err != nil {
			t.Fatal(err)
		}
	}
	if item := p.item(1); item.State != library.TranscodeNotNeeded {
		t.Errorf("item 1 = %+v, want not_needed", item)
	}
	if item := p.item(2); item.State != library.TranscodeBlocked || item.FailureCode != "linked_copies" {
		t.Errorf("item 2 = %+v, want blocked with linked_copies", item)
	}
}

func filmSource() transcode.File {
	return transcode.File{
		Container: "matroska,webm", Duration: 20 * time.Second, BitrateBps: 4_000_000,
		Streams: []transcode.Stream{
			{Index: 0, Type: transcode.StreamVideo, Codec: "h264", Width: 1920, Height: 1080, FrameRate: 30, BitrateBps: 3_800_000, BitDepth: 8},
			{Index: 1, Type: transcode.StreamAudio, Codec: "aac", Language: "en", Channels: 2, SampleRate: 48000, BitrateBps: 128_000},
		},
	}
}

func mp4Result(size int) transcode.File {
	return transcode.File{
		Container: "mov,mp4,m4a,3gp,3g2,mj2", Duration: 20 * time.Second, SizeBytes: int64(size), BitrateBps: int64(size) * 8 / 20,
		Streams: []transcode.Stream{
			{Index: 0, Type: transcode.StreamVideo, Codec: "hevc", Width: 1920, Height: 1080, FrameRate: 30, BitrateBps: 1_500_000, BitDepth: 8},
			{Index: 1, Type: transcode.StreamAudio, Codec: "aac", Language: "en", Channels: 2, SampleRate: 48000, BitrateBps: 128_000},
		},
	}
}

func gpuFilm(t *testing.T, fallback bool) *pipeline {
	t.Helper()
	hardware := fakeHW{transcode.Software: {Working: true, TenBit: true}, transcode.QSV: {Working: true, TenBit: true}}
	p := newPipeline(t, movieTarget(), hardware)
	p.settings.setup = func(setup settings.TranscodeSetup) settings.TranscodeSetup {
		setup.Encode.AllowCPUFallback = fallback
		return setup
	}
	p.add(t, 1, "Film/f.mkv", "video", filmSource(), 10_000_000)
	return p
}

func scriptedStarts(t *testing.T, p *pipeline, runs ...func(out string) transcode.FakeRun) {
	t.Helper()
	call := 0
	p.starter.Next = func(args []string) transcode.FakeRun {
		run := runs[min(call, len(runs)-1)]
		call++
		return run(args[len(args)-1])
	}
}

func TestRunFile_AGPUStepThatFailsAtTwoPercentHandsTheFileToTheSoftwareStep(t *testing.T) {
	p := gpuFilm(t, true)
	scriptedStarts(t, p,
		func(string) transcode.FakeRun {
			return transcode.FakeRun{Err: &transcode.RunError{ExitCode: 1, LastLine: "no device"}, Reports: []transcode.Progress{{OutTime: 400 * time.Millisecond}}}
		},
		func(out string) transcode.FakeRun {
			p.writeFile(t, out, 4_000_000)
			p.prober.set(out, mp4Result(4_000_000))
			return transcode.FakeRun{Reports: []transcode.Progress{{OutTime: 20 * time.Second}}}
		})
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	starts := p.starter.Started()
	if item := p.item(1); item.State != library.TranscodeDone || item.Engine != "software" || len(starts) != 2 {
		t.Errorf("item = %+v after %d starts, want done by the software step after two", item, len(starts))
	}
	if joined := strings.Join(starts[0], " "); !strings.Contains(joined, "hevc_qsv") || strings.Contains(strings.Join(starts[1], " "), "hevc_qsv") {
		t.Errorf("first start %q, second %q; want QSV first and software second", joined, strings.Join(starts[1], " "))
	}
}

func TestRunFile_AGPUStepThatFailsHalfWayIsAnErrorNotAStepDown(t *testing.T) {
	p := gpuFilm(t, true)
	scriptedStarts(t, p, func(string) transcode.FakeRun {
		return transcode.FakeRun{Err: &transcode.RunError{ExitCode: 1, LastLine: "gone"}, Reports: []transcode.Progress{{OutTime: 10 * time.Second}}}
	})
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := p.item(1); item.State != library.TranscodeNeeded || item.Attempts != 1 || len(p.starter.Started()) != 1 {
		t.Errorf("item = %+v after %d starts, want one attempt spent and no second engine", item, len(p.starter.Started()))
	}
}

func TestRunFile_WithoutTheCPUFallbackAFailedGPUStepEndsTheRunAndABrokenEngineFailsTheFile(t *testing.T) {
	p := gpuFilm(t, false)
	scriptedStarts(t, p, func(string) transcode.FakeRun {
		return transcode.FakeRun{Err: &transcode.RunError{ExitCode: 1, LastLine: "no device"}, Reports: []transcode.Progress{{OutTime: 400 * time.Millisecond}}}
	})
	if err := p.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if len(p.starter.Started()) != 1 {
		t.Errorf("started %d times, want only the GPU step", len(p.starter.Started()))
	}
	broken := newPipeline(t, movieTarget(), fakeHW{transcode.Software: {Working: false}})
	broken.add(t, 1, "Film/f.mkv", "video", filmSource(), 10_000_000)
	if err := broken.run(t, 1); err != nil {
		t.Fatal(err)
	}
	if item := broken.item(1); item.State != library.TranscodeFailed || item.FailureCode != "engine_unavailable" {
		t.Errorf("item = %+v, want failed with engine_unavailable", item)
	}
}

func TestFreshPlan_ReadsTheFileNowAndLeavesRunningAndFinishedItemsAlone(t *testing.T) {
	p := musicPipeline(t)
	needed(p, 1, "Album/t1.flac", false)
	fresh, ok, err := p.svc.FreshPlan(context.Background(), 1)
	if err != nil || !ok || fresh.State != "needed" || fresh.PlanJSON == "" {
		t.Fatalf("fresh = %+v, %v, %v; want a needed plan from the file as it is now", fresh, ok, err)
	}
	p.prober.set("/lib/Album/t1.flac", m4aResult(800_000))
	if fresh, ok, _ = p.svc.FreshPlan(context.Background(), 1); !ok || fresh.State != "not_needed" {
		t.Errorf("after the file changed: %+v, %v; want not_needed", fresh, ok)
	}
	for _, state := range []library.TranscodeState{library.TranscodeRunning, library.TranscodeDone} {
		p.items.items[1].State = state
		if _, ok, _ := p.svc.FreshPlan(context.Background(), 1); ok {
			t.Errorf("state %s got a fresh plan; want the stored one", state)
		}
	}
}
