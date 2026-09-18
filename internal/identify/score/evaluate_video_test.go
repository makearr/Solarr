package score

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
)

// seriesStandardProfileJSON is ADR-40's own worked example, translated verbatim
// from its YAML form to this package's JSON schema.
const seriesStandardProfileJSON = `{
  "quality_profile": "series-standard",
  "require": {
    "resolution": {"min": "720p", "max": "2160p"},
    "audio_language": {"any_of": ["en", "de"]},
    "bitrate": "reject_below_tier_min",
    "reject_codecs": ["xvid", "divx"]
  },
  "compare": ["resolution", "video_codec", "bitrate_distance", "audio_channels", "audio_codec", "sample_rate", "frame_rate", "score"],
  "tiers": {
    "resolution": [["2160p"], ["1080p", "1080i"], ["720p"], ["576p", "480p"]],
    "video_codec": [["av1", "hevc"], ["h264"], ["vp9"], ["mpeg4", "xvid"]],
    "audio_codec": [["truehd", "dtshd"], ["eac3", "dts"], ["aac", "ac3"], ["mp3"]],
    "audio_channels": [["7.1"], ["5.1"], ["2.0"]]
  },
  "targets": {
    "bitrate": [
      {"when": {"resolution": "2160p", "video_codec": "hevc"}, "min": "20Mbps", "ideal": "45Mbps", "max": "90Mbps", "weights": {"over": 1.0, "under": 1.0}},
      {"when": {"resolution": "1080p", "video_codec": "h264"}, "min": "6Mbps", "ideal": "12Mbps", "max": "25Mbps", "weights": {"over": 1.0, "under": 1.0}}
    ]
  },
  "scoring": [
    {"name": "preferred-groups", "score": 10, "specs": [
        {"field": "release_group", "in": "$preferred"}
    ]},
    {"name": "remux-not-upscaled", "score": 15, "specs": [
        {"field": "edition_tokens", "has": "remux", "required": true},
        {"field": "title", "matches": "(?i)upscal", "negate": true, "required": true}
    ]},
    {"name": "healthy-swarm", "score": 9, "specs": [
        {"field": "seeders", "gte": 20},
        {"field": "protocol", "eq": "usenet"}
    ]}
  ],
  "upgrade": {"min_delta": 15, "min_file_age": "24h", "max_per_window": 3, "window": "168h"}
}`

func seriesStandardProfile(t *testing.T) Profile {
	t.Helper()
	profile, err := Parse([]byte(seriesStandardProfileJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return profile
}

// TestSeriesStandard_ParsesTheUpgradeBlockWithoutEvaluatingIt confirms the parsed
// struct exists for a future caller, even though this pass evaluates none of it.
func TestSeriesStandard_ParsesTheUpgradeBlockWithoutEvaluatingIt(t *testing.T) {
	profile := seriesStandardProfile(t)
	if profile.Upgrade.MinDelta != 15 || profile.Upgrade.MaxPerWindow != 3 {
		t.Errorf("Upgrade = %+v, want MinDelta=15 MaxPerWindow=3", profile.Upgrade)
	}
}

// TestSeriesStandard_HEVCBeatsH264WhenResolutionTies is the design doc's own
// explainability example: candidates agree at 2160p, decided at video_codec instead.
func TestSeriesStandard_HEVCBeatsH264WhenResolutionTies(t *testing.T) {
	profile := seriesStandardProfile(t)

	hevc := Facts{File: FileFacts{
		Resolution: "2160p", VideoCodec: "hevc", AudioCodec: "dts", AudioChannels: "5.1",
		SampleRate: 48000, FrameRate: 23.976, OverallBitrate: 45_000_000,
		AudioLanguages: []string{"en"},
	}}
	h264 := Facts{File: FileFacts{
		Resolution: "2160p", VideoCodec: "h264", AudioCodec: "dts", AudioChannels: "5.1",
		SampleRate: 48000, FrameRate: 23.976, OverallBitrate: 45_000_000,
		AudioLanguages: []string{"en"},
	}}

	hevcDecision := Evaluate(profile, hevc, nil)
	h264Decision := Evaluate(profile, h264, nil)

	if hevcDecision.Outcome != model.OutcomeAccept {
		t.Fatalf("HEVC candidate Outcome = %v, want ACCEPT (%+v)", hevcDecision.Outcome, hevcDecision.Gates)
	}
	if h264Decision.Outcome != model.OutcomeAccept {
		t.Fatalf("H264 candidate Outcome = %v, want ACCEPT (%+v)", h264Decision.Outcome, h264Decision.Gates)
	}

	winner, decidedAtTier := model.Compare(hevcDecision.Quality, h264Decision.Quality)
	if winner != -1 {
		t.Errorf("Compare winner = %d, want -1 (HEVC candidate wins)", winner)
	}
	if decidedAtTier != 1 {
		t.Errorf("decidedAtTier = %d, want 1 (video_codec is index 1 in compare)", decidedAtTier)
	}
	if profile.Compare[decidedAtTier] != "video_codec" {
		t.Fatalf("compare[%d] = %q, want video_codec", decidedAtTier, profile.Compare[decidedAtTier])
	}

	resolutionTier := hevcDecision.Quality.Tiers[0]
	if resolutionTier.Rank != h264Decision.Quality.Tiers[0].Rank {
		t.Errorf("resolution tier should agree between both candidates: %+v vs %+v", resolutionTier, h264Decision.Quality.Tiers[0])
	}
	if got := hevcDecision.Quality.Tiers[1].Label; got != "hevc" {
		t.Errorf("HEVC candidate video_codec label = %q, want hevc", got)
	}
}

// TestSeriesStandard_HardGatesRejectRegardlessOfQuality proves a 2160p HEVC file
// with a disallowed audio language is still rejected -- stage 1 outranks stage 2.
func TestSeriesStandard_HardGatesRejectRegardlessOfQuality(t *testing.T) {
	profile := seriesStandardProfile(t)
	facts := Facts{File: FileFacts{
		Resolution: "2160p", VideoCodec: "hevc", OverallBitrate: 45_000_000,
		AudioLanguages: []string{"ru"},
	}}
	decision := Evaluate(profile, facts, nil)
	if decision.Outcome != model.OutcomeReject {
		t.Fatalf("Outcome = %v, want REJECT", decision.Outcome)
	}
	if len(decision.Rejections) != 1 || decision.Rejections[0].Rule != "audio_language" {
		t.Errorf("Rejections = %+v, want exactly one for audio_language", decision.Rejections)
	}
}

// TestSeriesStandard_AdditiveScoreOnlyBreaksTies matches the *Arr Custom Format
// rules against the remux/preferred-group/swarm facts the design doc's own example uses.
func TestSeriesStandard_AdditiveScoreOnlyBreaksTies(t *testing.T) {
	profile := seriesStandardProfile(t)
	facts := Facts{
		Release: ReleaseFacts{Title: "Show S01 2160p Remux", Seeders: 25, Protocol: "usenet"},
		File:    FileFacts{ReleaseGroup: "TRUSTEDGROUP", EditionTokens: []string{"remux"}},
	}
	decision := Evaluate(profile, facts, []string{"TRUSTEDGROUP"})

	if len(decision.Components) != 3 {
		t.Fatalf("Components = %+v, want 3 (preferred-groups, remux-not-upscaled, healthy-swarm)", decision.Components)
	}
	if decision.Score() != 34 {
		t.Errorf("Score() = %v, want 34", decision.Score())
	}
}

func TestSeriesStandard_NegatedSpecStopsTheRuleWhenTitleMatches(t *testing.T) {
	profile := seriesStandardProfile(t)
	facts := Facts{
		Release: ReleaseFacts{Title: "Show S01 2160p Remux Upscaled"},
		File:    FileFacts{EditionTokens: []string{"remux"}},
	}
	decision := Evaluate(profile, facts, nil)
	for _, component := range decision.Components {
		if component.Rule == "remux-not-upscaled" {
			t.Errorf("remux-not-upscaled fired for an upscaled title: %+v", decision.Components)
		}
	}
}
