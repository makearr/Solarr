package settings_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func videoTarget(codec, resolution string, crf float64) settings.QualityTarget {
	return settings.QualityTarget{Name: codec + resolution, MediaType: "video", VideoCodec: codec, Resolution: resolution, CRF: crf, Container: "mkv", AudioCodec: "aac", TolerancePct: 10}
}

func TestDefaultQualityFollowsCodecAndResolutionAndTheTargetsCRFWins(t *testing.T) {
	cases := []struct {
		codec, resolution string
		crf, want         float64
	}{
		{"hevc", "480p", 0, 26}, {"hevc", "720p", 0, 27}, {"hevc", "1080p", 0, 28}, {"hevc", "2160p", 0, 28}, {"hevc", "4k", 0, 28},
		{"h264", "576p", 0, 21}, {"h264", "720p", 0, 22}, {"h264", "1080p", 0, 23}, {"h264", "1440p", 0, 23},
		{"av1", "480p", 0, 32}, {"av1", "720p", 0, 33}, {"av1", "1080p", 0, 34}, {"av1", "2160p", 0, 34},
		{"vp9", "480p", 0, 29}, {"vp9", "720p", 0, 30}, {"vp9", "1080p", 0, 31}, {"vp9", "2160p", 0, 31},
		{"hevc", "", 0, 28}, {"hevc", "1080p", 24.5, 24.5}, {"mpeg2", "1080p", 0, 0},
	}
	for _, one := range cases {
		if got := settings.DefaultTranscodeSetup(videoTarget(one.codec, one.resolution, one.crf)).Encode.Quality; got != one.want {
			t.Errorf("%s %s crf %v: quality %v, want %v", one.codec, one.resolution, one.crf, got, one.want)
		}
	}
}

func TestDefaultSetupTakesThePresetAndDeinterlaceFromTheTargetAndLeavesAudioTargetsWithoutVideoParts(t *testing.T) {
	target := videoTarget("hevc", "1080p", 0)
	target.EncoderPreset, target.Deinterlace = "Slow", true
	got := settings.DefaultTranscodeSetup(target)
	if got.Encode.Preset != settings.PresetSlow || !got.Video.Deinterlace || got.Encode.Engine != settings.EngineAuto || !got.Encode.AllowCPUFallback {
		t.Errorf("video default = %+v", got)
	}
	target.EncoderPreset = "placebo"
	if got := settings.DefaultTranscodeSetup(target).Encode.Preset; got != settings.PresetSlower {
		t.Errorf("preset %q, want slower", got)
	}
	audio := settings.DefaultTranscodeSetup(settings.QualityTarget{Name: "Lossy", MediaType: "music", AudioCodec: "aac", Container: "m4a"})
	if audio.Video != (settings.VideoFilters{}) || audio.Encode.Preset != "" || audio.Encode.Quality != 0 || !audio.Audio.Normalize {
		t.Errorf("audio default = %+v", audio)
	}
	if problems := audio.Validate(settings.QualityTarget{MediaType: "music"}); len(problems) != 0 {
		t.Errorf("audio default problems %v", problems)
	}
}

func TestParseKeepsDefaultsForMissingFieldsAndRefusesUnknownOnes(t *testing.T) {
	target := videoTarget("hevc", "1080p", 0)
	got, err := settings.ParseTranscodeSetup([]byte(`{"version":1,"encode":{"quality":30},"skip":{"range_pct":10}}`), target)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.DefaultTranscodeSetup(target)
	want.Encode.Quality, want.Skip.RangePct = 30, 10
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed = %+v\nwant %+v", got, want)
	}
	if _, err := settings.ParseTranscodeSetup([]byte(`{"encode":{"qualty":30}}`), target); err == nil {
		t.Error("an unknown field was accepted")
	}
}

func TestValidateNamesEveryOutOfRangeField(t *testing.T) {
	target := videoTarget("hevc", "1080p", 0)
	setup := settings.DefaultTranscodeSetup(target)
	if problems := setup.Validate(target); len(problems) != 0 {
		t.Fatalf("default has problems %v", problems)
	}
	setup.Encode.Quality, setup.Encode.Preset, setup.Skip.RangePct, setup.Check.DurationSeconds = 64, "placebo", 0, 61
	setup.Ladder.ExtraPasses, setup.Streams.VideoCopyMaxPct, setup.Video.Denoise = 5, 1001, "huge"
	var paths []string
	for _, problem := range setup.Validate(target) {
		paths = append(paths, problem.Path)
	}
	want := "encode.preset encode.quality ladder.extra_passes skip.range_pct streams.video_copy_max_pct check.duration_seconds video_filters.denoise"
	for _, path := range strings.Fields(want) {
		found := false
		for _, got := range paths {
			found = found || got == path
		}
		if !found {
			t.Errorf("no problem for %s in %v", path, paths)
		}
	}
	if len(paths) != 7 {
		t.Errorf("problems %v", paths)
	}
}

func TestASetupRoundTripsItsRevisionGoesUpOnSaveAndResetAndItFollowsTheTarget(t *testing.T) {
	cache, cl := newCache(t)
	target, err := cache.CreateQualityTarget(t.Context(), videoTarget("hevc", "1080p", 0))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := cache.TranscodeSetup(t.Context(), target.ID)
	if err != nil || fresh.Stored || fresh.Revision != 0 || fresh.Setup.Encode.Quality != 28 {
		t.Fatalf("fresh = %+v, %v", fresh, err)
	}
	setup := fresh.Setup
	setup.Encode.Quality, setup.Skip.AlsoVideoCodecs = 25, []string{"h264"}
	for revision := int64(1); revision <= 2; revision++ {
		if err := cache.SetTranscodeSetup(t.Context(), cl, target.ID, setup); err != nil {
			t.Fatal(err)
		}
		if got, err := cache.TranscodeSetup(t.Context(), target.ID); err != nil || !got.Stored || got.Revision != revision || !reflect.DeepEqual(got.Setup, setup) {
			t.Fatalf("saved %d = %+v, %v", revision, got, err)
		}
	}
	if err := cache.ResetTranscodeSetup(t.Context(), cl, target.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := cache.TranscodeSetup(t.Context(), target.ID); got.Revision != 3 || got.Setup.Encode.Quality != 28 {
		t.Errorf("after reset = %+v", got)
	}
	setup.Encode.Quality = 99
	var problems config.Problems
	if err := cache.SetTranscodeSetup(t.Context(), cl, target.ID, setup); !errors.As(err, &problems) {
		t.Errorf("invalid setup saved: %v", err)
	}
	if _, err := cache.TranscodeSetup(t.Context(), 999999); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("unknown target: %v", err)
	}
	if err := cache.DeleteQualityTarget(t.Context(), target.ID); err != nil {
		t.Fatal(err)
	}
	if rows, err := cache.ListTranscodeSetupRevisions(t.Context()); err != nil || len(rows) != 0 {
		t.Errorf("rows after target delete = %v, %v", rows, err)
	}
}

func TestTranscodeGlobalDefaultsRoundTripAndRefuseOutOfRangeLimits(t *testing.T) {
	cache, cl := newCache(t)
	want := settings.TranscodeGlobal{
		Parallel: settings.LaneLimits{CPU: 1, GPU: 2, Audio: 4, Books: 4}, PlaybackParallel: settings.LaneLimits{GPU: 1, Audio: 1, Books: 1}, Originals: settings.OriginalsPolicy{RetainDays: 7, CapGB: 100},
		ParallelAuto: true, PlaybackParallelAuto: true,
	}
	if got := cache.Snapshot().TranscodeGlobal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default = %+v", got)
	}
	want = settings.TranscodeGlobal{
		Enabled: true, Parallel: settings.LaneLimits{CPU: 2, GPU: 3, Audio: 8, Books: 9}, PlaybackParallel: settings.LaneLimits{CPU: 1, GPU: 1, Audio: 1, Books: 1}, Originals: settings.OriginalsPolicy{RetainDays: 0, CapGB: 0},
	}
	if err := cache.SetTranscodeGlobal(t.Context(), cl, want); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().TranscodeGlobal(); !reflect.DeepEqual(got, want) || !cache.Snapshot().TranscodeEnabled() {
		t.Errorf("saved = %+v", got)
	}
	want.Parallel.CPU = 17
	if err := cache.SetTranscodeGlobal(t.Context(), cl, want); err == nil {
		t.Error("17 parallel conversions accepted")
	}
	want.Parallel = settings.LaneLimits{CPU: 3, GPU: 2, Audio: 4, Books: 4}
	if err := cache.SetTranscodeGlobal(t.Context(), cl, want); err == nil {
		t.Error("a GPU limit under the CPU limit accepted: each limit counts the lanes before it")
	}
}

func TestACategoryRowDefaultsToOffRoundTripsValidatesAndFollowsTheCategory(t *testing.T) {
	cache, _ := newCache(t)
	films, _ := cache.CreateCategory(t.Context(), "movie", "Films", 10)
	rows, err := cache.CategoryTranscodes(t.Context())
	if err != nil || !reflect.DeepEqual(rows, []settings.CategoryTranscode{{CategoryID: films.ID, Order: settings.OrderFound}}) {
		t.Fatalf("default rows = %+v, %v", rows, err)
	}
	want := settings.CategoryTranscode{CategoryID: films.ID, Enabled: true, Priority: -5, Order: settings.OrderLargest}
	if err := cache.SetCategoryTranscode(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	if rows, _ := cache.CategoryTranscodes(t.Context()); !reflect.DeepEqual(rows, []settings.CategoryTranscode{want}) {
		t.Errorf("saved rows = %+v", rows)
	}
	bad := want
	bad.Priority, bad.Order = 101, "random"
	var problems config.Problems
	if err := cache.SetCategoryTranscode(t.Context(), bad); !errors.As(err, &problems) || len(problems) != 2 {
		t.Errorf("bad row: %v", err)
	}
	if err := cache.SetCategoryTranscode(t.Context(), settings.CategoryTranscode{CategoryID: 999999, Order: settings.OrderFound}); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("unknown category: %v", err)
	}
	if err := cache.DeleteCategory(t.Context(), films.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := cache.CategoryTranscodes(t.Context()); len(rows) != 0 {
		t.Errorf("rows after delete = %+v", rows)
	}
}

func TestTranscodeExportCarriesPolicyRowsAndSavedSetupsToAnotherInstanceAndRefusesABadSetup(t *testing.T) {
	source, cl := newCache(t)
	films, _ := source.CreateCategory(t.Context(), "movie", "Films", 10)
	global := settings.TranscodeGlobal{Enabled: true, Parallel: settings.LaneLimits{CPU: 2, GPU: 3, Audio: 5, Books: 5}, Originals: settings.OriginalsPolicy{RetainDays: 3, CapGB: 50}}
	if err := source.SetTranscodeGlobal(t.Context(), cl, global); err != nil {
		t.Fatal(err)
	}
	row := settings.CategoryTranscode{CategoryID: films.ID, Enabled: true, Priority: 4, Order: settings.OrderNewest}
	if err := source.SetCategoryTranscode(t.Context(), row); err != nil {
		t.Fatal(err)
	}
	stored, err := source.TranscodeSetup(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	stored.Setup.Encode.Quality = 19
	if err := source.SetTranscodeSetup(t.Context(), cl, 1, stored.Setup); err != nil {
		t.Fatal(err)
	}
	var document settings.Document
	if err := source.ExportTranscode(t.Context(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Transcode.Setups) != 1 || len(document.Transcode.Libraries) != 1 {
		t.Fatalf("exported = %+v", document.Transcode)
	}

	target, _ := newCache(t)
	other, _ := target.CreateCategory(t.Context(), "movie", "Films", 10)
	if err := target.ImportTranscode(t.Context(), cl, document); err != nil {
		t.Fatal(err)
	}
	if got := target.Snapshot().TranscodeGlobal(); !reflect.DeepEqual(got, global) {
		t.Errorf("global = %+v", got)
	}
	row.CategoryID = other.ID
	if rows, _ := target.CategoryTranscodes(t.Context()); !reflect.DeepEqual(rows, []settings.CategoryTranscode{row}) {
		t.Errorf("rows = %+v", rows)
	}
	if got, _ := target.TranscodeSetup(t.Context(), 1); got.Setup.Encode.Quality != 19 || got.Revision != 1 {
		t.Errorf("setup = %+v", got)
	}

	document.Transcode.Setups[0].Setup["ladder"] = map[string]any{"extra_passes": 9}
	third, _ := newCache(t)
	var problems config.Problems
	if err := third.ImportTranscode(t.Context(), cl, document); !errors.As(err, &problems) {
		t.Errorf("bad setup: %v", err)
	}
	if third.Snapshot().TranscodeEnabled() {
		t.Error("a refused section changed the policy")
	}
}

func TestTranscodeWarningsNameWhereASetupMissesItsTarget(t *testing.T) {
	bitrate := int64(2500)
	target := settings.QualityTarget{MediaType: "video", VideoCodec: "hevc", CRF: 24, VideoBitrateKbps: &bitrate}
	setup := settings.DefaultTranscodeSetup(target)
	if got := settings.TranscodeWarnings(setup, target); len(got) != 0 {
		t.Errorf("default = %+v", got)
	}
	setup.Encode.Quality = 28
	setup.Check.WithinRange, setup.Check.Smaller = false, false
	target.VideoBitrateKbps = nil
	want := []settings.TranscodeWarning{{Code: settings.WarnQualityDiffersFromTarget, Value: "28"}, {Code: settings.WarnChecksOff}, {Code: settings.WarnRangeUnknown}}
	if got := settings.TranscodeWarnings(setup, target); !reflect.DeepEqual(got, want) {
		t.Errorf("warnings = %+v, want %+v", got, want)
	}
}

func TestTranscodeWarningsBitrateAndResultsOutsideTheRange(t *testing.T) {
	bitrate := int64(2000)
	target := settings.QualityTarget{MediaType: "video", VideoCodec: "hevc", VideoBitrateKbps: &bitrate, TolerancePct: 20}
	setup := settings.DefaultTranscodeSetup(target)
	setup.Encode.RateMode, setup.Encode.VideoBitrateKbps = settings.RateBitrate, 2300
	if got := settings.TranscodeWarnings(setup, target); len(got) != 0 {
		t.Errorf("inside = %+v", got)
	}
	setup.Encode.VideoBitrateKbps = 2500
	want := []settings.TranscodeWarning{{Code: settings.WarnBitrateOutsideRange, Value: "2500"}}
	if got := settings.TranscodeWarnings(setup, target); !reflect.DeepEqual(got, want) {
		t.Errorf("above = %+v", got)
	}
	high := []int64{150, 150, 150, 150, 150, 150, 150, 150, 150, 90}
	if got, ok := settings.TranscodeResultsWarning(high, target); !ok || got != (settings.TranscodeWarning{Code: settings.WarnResultsOutsideRange, Value: "150"}) {
		t.Errorf("results = %+v %v", got, ok)
	}
	if _, ok := settings.TranscodeResultsWarning(high[:9], target); ok {
		t.Error("nine files are too few to judge")
	}
	if _, ok := settings.TranscodeResultsWarning([]int64{95, 100, 105, 110, 90, 100, 100, 100, 100, 100}, target); ok {
		t.Error("a median inside the range warned")
	}
}

func TestTranscodeWarningsOfAComicOrEbookNeverAskForABitrate(t *testing.T) {
	setup := settings.TranscodeSetup{}
	for _, kind := range []string{"comic", "ebook"} {
		if got := settings.TranscodeWarningsOf(kind, setup, settings.QualityTarget{MediaType: "book", Container: "cbz"}); len(got) != 0 {
			t.Errorf("%s: warnings = %v, want none", kind, got)
		}
	}
	if got := settings.TranscodeWarningsOf("audio", setup, settings.QualityTarget{MediaType: "music"}); len(got) == 0 {
		t.Error("an audio target without a bitrate lost its warning")
	}
}

func TestAutoLaneLimitsFollowTheHardware(t *testing.T) {
	for _, check := range []struct {
		name            string
		hardware        settings.LaneHardware
		normal, playing settings.LaneLimits
	}{
		{"two engines, eight cores", settings.LaneHardware{Cores: 8, EncodeEngines: 2}, settings.LaneLimits{CPU: 1, GPU: 2, Audio: 4, Books: 4}, settings.LaneLimits{CPU: 0, GPU: 1, Audio: 2, Books: 2}},
		{"no hardware encoder is Video HW as Video", settings.LaneHardware{Cores: 4}, settings.LaneLimits{CPU: 1, GPU: 1, Audio: 2, Books: 2}, settings.LaneLimits{CPU: 0, GPU: 0, Audio: 1, Books: 1}},
		{"one core still lets audio run", settings.LaneHardware{Cores: 1}, settings.LaneLimits{CPU: 1, GPU: 1, Audio: 1, Books: 1}, settings.LaneLimits{CPU: 0, GPU: 0, Audio: 1, Books: 1}},
		{"engines above the cores raise audio to keep the order", settings.LaneHardware{Cores: 2, EncodeEngines: 3}, settings.LaneLimits{CPU: 1, GPU: 3, Audio: 3, Books: 3}, settings.LaneLimits{CPU: 0, GPU: 2, Audio: 2, Books: 2}},
		{"nothing passes sixteen", settings.LaneHardware{Cores: 128, EncodeEngines: 20}, settings.LaneLimits{CPU: 1, GPU: 16, Audio: 16, Books: 16}, settings.LaneLimits{CPU: 0, GPU: 16, Audio: 16, Books: 16}},
	} {
		normal, playing := settings.AutoLaneLimits(check.hardware)
		if normal != check.normal || playing != check.playing {
			t.Errorf("%s: normal = %+v, playing = %+v, want %+v and %+v", check.name, normal, playing, check.normal, check.playing)
		}
	}
}

func TestResolvedReplacesOnlyTheRowsThatFollowTheHardware(t *testing.T) {
	manual := settings.LaneLimits{CPU: 2, GPU: 2, Audio: 2, Books: 2}
	global := settings.TranscodeGlobal{Parallel: manual, PlaybackParallel: manual, ParallelAuto: true}
	got := global.Resolved(settings.LaneHardware{Cores: 8, EncodeEngines: 2})
	if got.Parallel != (settings.LaneLimits{CPU: 1, GPU: 2, Audio: 4, Books: 4}) || got.PlaybackParallel != manual {
		t.Errorf("resolved = %+v", got)
	}
}

func TestTranscodeLimitsSavedByAPersonStopFollowingTheHardware(t *testing.T) {
	cache, cl := newCache(t)
	saved := settings.TranscodeGlobal{Parallel: settings.LaneLimits{CPU: 1, GPU: 1, Audio: 1, Books: 1}, PlaybackParallelAuto: true, Originals: settings.OriginalsPolicy{RetainDays: 7, CapGB: 100}}
	if err := cache.SetTranscodeGlobal(t.Context(), cl, saved); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().TranscodeGlobal(); got.ParallelAuto || !got.PlaybackParallelAuto || got.Parallel != saved.Parallel {
		t.Errorf("saved = %+v", got)
	}
	saved.PlaybackParallelAuto, saved.PlaybackParallel = false, settings.LaneLimits{GPU: 2, Audio: 1}
	if err := cache.SetTranscodeGlobal(t.Context(), cl, saved); err == nil {
		t.Error("a manual row whose audio limit is under its GPU limit was accepted")
	}
}
