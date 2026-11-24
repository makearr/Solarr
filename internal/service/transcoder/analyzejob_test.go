package transcoder_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/transcode"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

type fakeItems struct {
	objects []library.ObjectToAnalyze
	facts   map[int64]library.ObjectFacts
	judged  []library.JudgedTranscodeItem
}

func (f *fakeItems) Judge(_ context.Context, judged []library.JudgedTranscodeItem) error {
	f.judged = append(f.judged, judged...)
	return nil
}

func (f *fakeItems) ObjectsToAnalyze(_ context.Context, _ []int64, _, _, afterID, pageSize int64) ([]library.ObjectToAnalyze, error) {
	var page []library.ObjectToAnalyze
	for _, object := range f.objects {
		if object.MediaObjectID > afterID && int64(len(page)) < pageSize {
			page = append(page, object)
		}
	}
	return page, nil
}

func (f *fakeItems) Facts(_ context.Context, ids []int64) (map[int64]library.ObjectFacts, error) {
	out := map[int64]library.ObjectFacts{}
	for _, id := range ids {
		out[id] = f.facts[id]
	}
	return out, nil
}

type fakeSettings struct {
	global settings.TranscodeGlobal
	target settings.QualityTarget
	on     bool
}

func (f fakeSettings) Global() settings.TranscodeGlobal { return f.global }
func (f fakeSettings) Libraries(context.Context) ([]settings.TranscodeLibrary, error) {
	target := f.target
	return []settings.TranscodeLibrary{{CategoryTranscode: settings.CategoryTranscode{CategoryID: 3, Enabled: f.on}, Target: &target}}, nil
}

func (f fakeSettings) Setup(context.Context, int64) (settings.StoredTranscodeSetup, error) {
	return settings.StoredTranscodeSetup{Setup: settings.DefaultTranscodeSetup(f.target), Revision: 4}, nil
}
func (f fakeSettings) RootFolders(int64) []settings.RootFolder { return []settings.RootFolder{{ID: 7}} }
func (f fakeSettings) Languages(int64) []string                { return nil }

func analyzeService(items *fakeItems, on, enabled bool) *transcoder.Service {
	target := musicTarget()
	target.ID = 9
	return &transcoder.Service{
		Items: items, Hardware: fakeHW{}, Clock: clock.NewFakeAtEpoch(),
		Settings: fakeSettings{global: settings.TranscodeGlobal{Enabled: enabled}, target: target, on: on},
	}
}

type fakeHW struct{}

func (fakeHW) EncoderFor(transcode.Accel, transcode.Codec) (transcode.EncoderSupport, bool) {
	return transcode.EncoderSupport{}, false
}

func (fakeHW) BestEngine(transcode.Codec, bool) (transcode.Accel, bool) { return "", false }

func TestAnalyze_JudgesEveryPageAndLeavesAFileTheProbeHasNotReadForNextTime(t *testing.T) {
	items := &fakeItems{facts: map[int64]library.ObjectFacts{}}
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
		items := &fakeItems{objects: []library.ObjectToAnalyze{{MediaObjectID: 1, Kind: "audio"}}, facts: map[int64]library.ObjectFacts{1: {Measured: true}}}
		if err := analyzeService(items, c.library, c.master).Analyze(t.Context(), 3, nil); err != nil || len(items.judged) != 0 {
			t.Errorf("master %v library %v: judged %d, err %v, want nothing", c.master, c.library, len(items.judged), err)
		}
	}
}
