package transcoder_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func analyzeService(items *memItems, library, master bool) *transcoder.Service {
	target := musicTarget()
	target.ID = 9
	return &transcoder.Service{
		Items: items, Hardware: fakeHW{}, Clock: clock.NewFakeAtEpoch(), Queue: &queuedJobs{},
		Settings: &memSettings{global: settings.TranscodeGlobal{Enabled: master}, target: target, enabled: library},
	}
}

func TestAnalyze_JudgesEveryPageAndLeavesAFileTheProbeHasNotReadForNextTime(t *testing.T) {
	items := newMemItems()
	for id := int64(1); id <= 201; id++ {
		items.objects = append(items.objects, library.ObjectToAnalyze{MediaObjectID: id, Kind: "audio", RootFolderID: 7})
		items.facts[id] = library.ObjectFacts{
			MediaObjectID: id, Measured: true, Container: "flac", AudioCodec: "flac", AudioChannels: "2.0", SampleRate: 44100, DurationMs: 200000, SizeBytes: 30_000_000, OverallBitrate: 1_000_000,
		}
	}
	items.facts[5] = library.ObjectFacts{MediaObjectID: 5}
	items.facts[6] = library.ObjectFacts{
		MediaObjectID: 6, Measured: true, Container: "mov,mp4,m4a,3gp", AudioCodec: "aac", AudioChannels: "2.0", SampleRate: 48000, DurationMs: 200000, SizeBytes: 3_000_000, OverallBitrate: 128_000,
	}
	if err := analyzeService(items, true, true).Analyze(t.Context(), 3, nil); err != nil {
		t.Fatal(err)
	}
	counts := map[library.TranscodeState]int{}
	for _, item := range items.judged {
		counts[item.State]++
		if item.TargetID != 9 || item.SetupRevision != 4 || item.CategoryID != 3 {
			t.Fatalf("item %+v carries the wrong target, revision or library", item)
		}
	}
	if counts[library.TranscodeNeeded] != 199 || counts[library.TranscodeNotNeeded] != 1 || len(items.judged) != 200 {
		t.Errorf("states = %v over %d items, want 199 needed, 1 not_needed, file 5 skipped", counts, len(items.judged))
	}
	if first := items.judged[0]; first.Lane != "audio" || first.PlanJSON == "" || first.Kind != "audio" {
		t.Errorf("first item = %+v, want an audio lane with its plan stored", first)
	}
}

func TestAnalyze_DoesNothingWhileTheMasterOrTheLibraryIsOff(t *testing.T) {
	for _, c := range []struct{ master, library bool }{{false, true}, {true, false}} {
		items := newMemItems()
		items.objects = []library.ObjectToAnalyze{{MediaObjectID: 1, Kind: "audio"}}
		items.facts[1] = library.ObjectFacts{Measured: true}
		if err := analyzeService(items, c.library, c.master).Analyze(t.Context(), 3, nil); err != nil || len(items.judged) != 0 {
			t.Errorf("master %v library %v: judged %d, err %v, want nothing", c.master, c.library, len(items.judged), err)
		}
	}
}

func TestAnalyze_StartsADispatchOnlyWhenItFoundWorkToDo(t *testing.T) {
	flac := library.ObjectFacts{MediaObjectID: 1, Measured: true, Container: "flac", AudioCodec: "flac", AudioChannels: "2.0", SampleRate: 44100, DurationMs: 200000, SizeBytes: 30_000_000, OverallBitrate: 1_000_000}
	aac := library.ObjectFacts{MediaObjectID: 2, Measured: true, Container: "mov,mp4,m4a,3gp", AudioCodec: "aac", AudioChannels: "2.0", SampleRate: 48000, DurationMs: 200000, SizeBytes: 3_000_000, OverallBitrate: 128_000}
	for _, c := range []struct {
		facts library.ObjectFacts
		want  int
	}{{flac, 1}, {aac, 0}} {
		items := newMemItems()
		items.objects = []library.ObjectToAnalyze{{MediaObjectID: c.facts.MediaObjectID, Kind: "audio", RootFolderID: 7}}
		items.facts[c.facts.MediaObjectID] = c.facts
		service := analyzeService(items, true, true)
		queue := service.Queue.(*queuedJobs)
		if err := service.Analyze(t.Context(), 3, nil); err != nil {
			t.Fatal(err)
		}
		if len(queue.requests) != c.want {
			t.Errorf("file %d: %d jobs queued, want %d", c.facts.MediaObjectID, len(queue.requests), c.want)
		}
	}
}
