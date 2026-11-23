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
		Parallel: settings.LaneLimits{CPU: 1, GPU: 2, Audio: 4}, PlaybackParallel: settings.LaneLimits{GPU: 1, Audio: 1}, Originals: settings.OriginalsPolicy{RetainDays: 7, CapGB: 100},
	}
	if got := cache.Snapshot().TranscodeGlobal(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default = %+v", got)
	}
	want = settings.TranscodeGlobal{
		Enabled: true, Parallel: settings.LaneLimits{CPU: 2, GPU: 3, Audio: 8}, PlaybackParallel: settings.LaneLimits{CPU: 1}, Originals: settings.OriginalsPolicy{RetainDays: 0, CapGB: 0},
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
