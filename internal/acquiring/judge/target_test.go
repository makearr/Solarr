package judge_test

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func TestWithTarget_TheTargetsResolutionAndCodecLeadTheRanking(t *testing.T) {
	rules := score.Profile{
		Compare: []string{"resolution", "source", "score"},
		Tiers: map[string][][]string{
			"resolution": {{"2160p"}, {"1080p", "1080i"}, {"720p"}},
			"source":     {{"webdl"}, {"hdtv"}},
		},
	}
	got := judge.WithTarget(rules, settings.QualityTarget{Resolution: "1080p", VideoCodec: "h265"})
	if !slices.Equal(got.Compare, []string{"resolution", "video_codec", "source", "score"}) {
		t.Errorf("compare = %v, want the target's dimensions first", got.Compare)
	}
	if first := got.Tiers["resolution"][0]; !slices.Equal(first, []string{"1080p"}) || !slices.Equal(got.Tiers["resolution"][2], []string{"1080i"}) {
		t.Errorf("resolution tiers = %v, want 1080p alone first and the rest after in their order", got.Tiers["resolution"])
	}
	if !slices.Equal(got.Tiers["video_codec"][0], []string{"h265"}) {
		t.Errorf("video codec tiers = %v", got.Tiers["video_codec"])
	}
	if len(rules.Tiers["resolution"]) != 3 || rules.Compare[0] != "resolution" {
		t.Error("the profile's own rules were changed")
	}
}

// An audio library's target leads its ranking too (2026-10-04): the codec it names is the best one, and the bitrate
// nearest to it wins among equals. A video target's audio codec is left to the profile.
func TestWithTarget_AnAudioTargetsCodecAndBitrateLeadTheRanking(t *testing.T) {
	rules := score.Profile{
		Compare: []string{"audio_codec", "score"},
		Tiers:   map[string][][]string{"audio_codec": {{"flac"}, {"mp3", "aac"}}},
	}
	bitrate := int64(256)
	got := judge.WithTarget(rules, settings.QualityTarget{AudioCodec: "aac", Container: "m4a", BitrateKbps: &bitrate})
	if !slices.Equal(got.Compare, []string{"audio_codec", "overall_bitrate_distance", "score"}) {
		t.Errorf("compare = %v, want the codec, then the distance from the target bitrate", got.Compare)
	}
	want := [][]string{{"aac"}, {"flac"}, {"mp3"}}
	if tiers := got.Tiers["audio_codec"]; len(tiers) != 3 || !slices.Equal(tiers[0], want[0]) || !slices.Equal(tiers[1], want[1]) || !slices.Equal(tiers[2], want[2]) {
		t.Errorf("audio codec tiers = %v, want %v", got.Tiers["audio_codec"], want)
	}
	if ideal := got.Targets["overall_bitrate"]; len(ideal) != 1 || ideal[0].Ideal != "256kbps" {
		t.Errorf("overall bitrate target = %+v, want 256kbps", ideal)
	}

	video := judge.WithTarget(rules, settings.QualityTarget{Resolution: "1080p", AudioCodec: "aac"})
	if tiers := video.Tiers["audio_codec"]; !slices.Equal(tiers[0], []string{"flac"}) {
		t.Errorf("a video target reordered the audio codecs: %v", tiers)
	}
}

func TestWithTarget_BitrateAndFrameRateRankByDistanceAndLanguagesScore(t *testing.T) {
	rules := score.Profile{Compare: []string{"resolution", "score"}, Tiers: map[string][][]string{"resolution": {{"1080p"}}}}
	video, audio := int64(4000), int64(192)
	got := judge.WithTarget(rules, settings.QualityTarget{
		Resolution: "1080p", VideoBitrateKbps: &video, AudioBitrateKbps: &audio, FrameRate: 24, AudioChannels: "5.1", AudioLanguages: []string{"de"},
	})
	for _, dimension := range []string{"video_bitrate_distance", "audio_bitrate_distance", "frame_rate_distance", "audio_channels"} {
		if !slices.Contains(got.Compare, dimension) {
			t.Errorf("compare = %v, want %s among the leading dimensions", got.Compare, dimension)
		}
	}
	if got.Targets["video_bitrate"][0].Ideal != "4000kbps" || got.Targets["frame_rate"][0].Ideal != "24" {
		t.Errorf("targets = %+v", got.Targets)
	}
	if len(got.Scoring) != 1 || got.Scoring[0].Specs[0].Has != "de" {
		t.Errorf("scoring = %+v, want the audio language as a score rule", got.Scoring)
	}
	closer := score.Facts{File: score.FileFacts{Resolution: "1080p", VideoBitrate: 4_100_000, AudioChannels: "5.1"}}
	far := score.Facts{File: score.FileFacts{Resolution: "1080p", VideoBitrate: 12_000_000, AudioChannels: "5.1"}}
	a, b := score.Evaluate(got, closer, nil), score.Evaluate(got, far, nil)
	if winner, _ := model.Compare(a.Quality, b.Quality); winner != -1 {
		t.Errorf("the release near the ideal bitrate did not outrank the one three times over it (winner %d)", winner)
	}
}
