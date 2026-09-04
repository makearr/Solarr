package score

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/model"
)

// musicStandardProfileJSON reorders ADR-40's own machinery for music/audiobooks --
// concrete proof the same Evaluate function runs unmodified on different profile data.
const musicStandardProfileJSON = `{
  "quality_profile": "music-standard",
  "require": {
    "reject_codecs": ["wma"]
  },
  "compare": ["lossless", "sample_rate", "bit_depth", "bitrate_distance", "audio_codec", "score"],
  "tiers": {
    "audio_codec": [["flac", "alac"], ["ogg"], ["mp3"]]
  },
  "targets": {
    "bitrate": [
      {"when": {"audio_codec": "mp3"}, "min": "192kbps", "ideal": "320kbps", "max": "400kbps"}
    ]
  },
  "scoring": [
    {"name": "preferred-groups", "score": 10, "specs": [{"field": "release_group", "in": "$preferred"}]}
  ]
}`

func musicStandardProfile(t *testing.T) Profile {
	t.Helper()
	profile, err := Parse([]byte(musicStandardProfileJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return profile
}

func TestMusicStandard_LosslessBeatsLossyAtTheFirstDimension(t *testing.T) {
	profile := musicStandardProfile(t)

	flac := Facts{File: FileFacts{AudioCodec: "flac", Lossless: true, SampleRate: 44100, BitDepth: 16}}
	mp3 := Facts{File: FileFacts{AudioCodec: "mp3", Lossless: false, SampleRate: 44100, OverallBitrate: 320_000}}

	flacDecision := Evaluate(profile, flac, nil)
	mp3Decision := Evaluate(profile, mp3, nil)

	winner, decidedAtTier := model.Compare(flacDecision.Quality, mp3Decision.Quality)
	if winner != -1 {
		t.Errorf("Compare winner = %d, want -1 (FLAC wins)", winner)
	}
	if decidedAtTier != 0 {
		t.Errorf("decidedAtTier = %d, want 0 (lossless is compare[0])", decidedAtTier)
	}
}

func TestMusicStandard_HigherSampleRateWinsWhenBothLossless(t *testing.T) {
	profile := musicStandardProfile(t)

	hiRes := Facts{File: FileFacts{AudioCodec: "flac", Lossless: true, SampleRate: 96000, BitDepth: 24}}
	cdQuality := Facts{File: FileFacts{AudioCodec: "flac", Lossless: true, SampleRate: 44100, BitDepth: 16}}

	hiResDecision := Evaluate(profile, hiRes, nil)
	cdDecision := Evaluate(profile, cdQuality, nil)

	winner, decidedAtTier := model.Compare(hiResDecision.Quality, cdDecision.Quality)
	if winner != -1 {
		t.Errorf("Compare winner = %d, want -1 (96kHz wins)", winner)
	}
	if decidedAtTier != 1 {
		t.Errorf("decidedAtTier = %d, want 1 (sample_rate is compare[1], lossless tied)", decidedAtTier)
	}
}

func TestMusicStandard_RejectCodecsStillGatesAMusicProfile(t *testing.T) {
	profile := musicStandardProfile(t)
	facts := Facts{File: FileFacts{AudioCodec: "wma"}}
	decision := Evaluate(profile, facts, nil)
	if decision.Outcome != model.OutcomeReject {
		t.Errorf("Outcome = %v, want REJECT", decision.Outcome)
	}
}

func TestMusicStandard_ScoringWorksUnmodifiedAcrossProfiles(t *testing.T) {
	profile := musicStandardProfile(t)
	facts := Facts{File: FileFacts{AudioCodec: "flac", ReleaseGroup: "TRUSTEDGROUP"}}
	decision := Evaluate(profile, facts, []string{"TRUSTEDGROUP"})
	if decision.Score() != 10 {
		t.Errorf("Score() = %v, want 10", decision.Score())
	}
}

// targetedProfileJSON is the user's own worked example (ADR-40 amended
// 2026-09-04): a profile that names an ideal rather than assuming "higher is
// always better" for sample rate and bit depth.
const targetedProfileJSON = `{
  "quality_profile": "moderate-not-maximal",
  "compare": ["sample_rate_distance", "bit_depth_distance", "score"],
  "targets": {
    "sample_rate": [{"ideal": "48kHz"}],
    "bit_depth": [{"ideal": "16"}]
  }
}`

// TestTargetedProfile_ModerateSpecBeatsMaximalSpec is todo 4.3's own acceptance
// criterion: a profile with an explicit sample-rate/bit-depth target must rank
// a 48kHz/16-bit file at its ideal above a 192kHz/24-bit file that overshoots
// it -- the exact case "higher is better" got wrong before this amendment.
func TestTargetedProfile_ModerateSpecBeatsMaximalSpec(t *testing.T) {
	profile, err := Parse([]byte(targetedProfileJSON))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	atIdeal := Facts{File: FileFacts{AudioCodec: "aac", SampleRate: 48000, BitDepth: 16}}
	overshoot := Facts{File: FileFacts{AudioCodec: "flac", Lossless: true, SampleRate: 192000, BitDepth: 24}}

	idealDecision := Evaluate(profile, atIdeal, nil)
	overshootDecision := Evaluate(profile, overshoot, nil)

	winner, decidedAtTier := model.Compare(idealDecision.Quality, overshootDecision.Quality)
	if winner != -1 {
		t.Errorf("Compare winner = %d, want -1 (the 48kHz/16-bit file at its ideal wins)", winner)
	}
	if decidedAtTier != 0 {
		t.Errorf("decidedAtTier = %d, want 0 (sample_rate_distance is compare[0])", decidedAtTier)
	}
}
