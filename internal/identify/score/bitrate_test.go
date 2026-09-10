package score

import "testing"

func TestParseBitRate(t *testing.T) {
	tests := []struct {
		raw  string
		want float64
	}{
		{"20Mbps", 20_000_000},
		{"6Mbps", 6_000_000},
		{"128kbps", 128_000},
		{"1MB/min", 1_000_000 * 8.0 / 60},
		{"500kB/min", 500_000 * 8.0 / 60},
		{"1500000bps", 1_500_000},
	}
	for _, test := range tests {
		got, err := parseBitRate(test.raw)
		if err != nil {
			t.Errorf("parseBitRate(%q): %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("parseBitRate(%q) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestParseBitRate_RejectsUnknownUnit(t *testing.T) {
	if _, err := parseBitRate("20Gbps"); err == nil {
		t.Fatal("parseBitRate(\"20Gbps\"): expected error, got nil")
	}
}

func TestParseSampleRate(t *testing.T) {
	tests := []struct {
		raw  string
		want float64
	}{
		{"48kHz", 48000}, {"44.1kHz", 44100}, {"96000", 96000}, {"44100Hz", 44100},
	}
	for _, test := range tests {
		got, err := parseSampleRate(test.raw)
		if err != nil {
			t.Errorf("parseSampleRate(%q): %v", test.raw, err)
			continue
		}
		if got != test.want {
			t.Errorf("parseSampleRate(%q) = %v, want %v", test.raw, got, test.want)
		}
	}
}

func TestParseSampleRate_RejectsGarbage(t *testing.T) {
	if _, err := parseSampleRate("not-a-rate"); err == nil {
		t.Fatal("parseSampleRate(\"not-a-rate\"): expected error, got nil")
	}
}

// TestTargetDistance_PenalizesBothDirections is ADR-40's worked example: against
// a 4 Mbit/s ideal, overshooting to 10 Mbit/s costs more than undershooting to 3.
func TestTargetDistance_PenalizesBothDirections(t *testing.T) {
	target := Target{Ideal: "4Mbps", Weights: TargetWeights{Over: 1, Under: 1}}
	facts := func(bps int64) Facts { return Facts{File: FileFacts{OverallBitrate: bps}} }

	over, ok := targetDistance("bitrate", target, true, facts(10_000_000))
	if !ok {
		t.Fatal("targetDistance(10Mbps): ok = false")
	}
	under, ok := targetDistance("bitrate", target, true, facts(3_000_000))
	if !ok {
		t.Fatal("targetDistance(3Mbps): ok = false")
	}

	if diff := over - 1.321928; diff > 0.001 || diff < -0.001 {
		t.Errorf("targetDistance(10Mbps) = %v, want ~1.321928", over)
	}
	if diff := under - 0.415037; diff > 0.001 || diff < -0.001 {
		t.Errorf("targetDistance(3Mbps) = %v, want ~0.415037", under)
	}
	if over <= under {
		t.Errorf("targetDistance(10Mbps)=%v should exceed targetDistance(3Mbps)=%v", over, under)
	}
}

func TestTargetDistance_WeightsScaleTheAsymmetry(t *testing.T) {
	target := Target{Ideal: "4Mbps", Weights: TargetWeights{Over: 2, Under: 1}}
	over, _ := targetDistance("bitrate", target, true, Facts{File: FileFacts{OverallBitrate: 8_000_000}})
	if diff := over - 2.0; diff > 0.001 || diff < -0.001 {
		t.Errorf("weighted overshoot distance = %v, want ~2.0 (raw 1.0 * weight 2)", over)
	}
}

func TestTargetDistance_NoTargetIsNotOK(t *testing.T) {
	if _, ok := targetDistance("bitrate", Target{}, false, Facts{File: FileFacts{OverallBitrate: 5_000_000}}); ok {
		t.Error("targetDistance with hasTarget=false: ok = true, want false")
	}
}

func TestTargetDistance_UnmeasuredRateIsNotOK(t *testing.T) {
	target := Target{Ideal: "4Mbps", Weights: TargetWeights{Over: 1, Under: 1}}
	if _, ok := targetDistance("bitrate", target, true, Facts{File: FileFacts{OverallBitrate: 0}}); ok {
		t.Error("targetDistance with 0 bits/sec: ok = true, want false")
	}
}

// TestTargetDistance_SampleRateOvershootCostsLessThanUndershoot is ADR-40's
// amendment itself: 96kHz against a 48kHz ideal, with the default asymmetric
// weights, must rank between the ideal and an equally-distant undershoot.
func TestTargetDistance_SampleRateOvershootCostsLessThanUndershoot(t *testing.T) {
	target := Target{Ideal: "48kHz"}
	defaultTargetWeights("sample_rate", &target.Weights)

	over, ok := targetDistance("sample_rate", target, true, Facts{File: FileFacts{SampleRate: 96000}})
	if !ok {
		t.Fatal("targetDistance(96kHz): ok = false")
	}
	under, ok := targetDistance("sample_rate", target, true, Facts{File: FileFacts{SampleRate: 24000}})
	if !ok {
		t.Fatal("targetDistance(24kHz): ok = false")
	}
	if over >= under {
		t.Errorf("targetDistance(96kHz)=%v should be less than targetDistance(24kHz)=%v (over-weight 0.5 < under-weight 1.0)", over, under)
	}
}

func TestMatchTarget_FirstMatchWins(t *testing.T) {
	targets := []Target{
		{When: map[string]string{"resolution": "2160p", "video_codec": "hevc"}, Ideal: "45Mbps"},
		{When: map[string]string{"resolution": "1080p", "video_codec": "h264"}, Ideal: "12Mbps"},
	}
	target, ok := matchTarget(targets, FileFacts{Resolution: "1080p", VideoCodec: "h264"})
	if !ok {
		t.Fatal("matchTarget: ok = false, want true")
	}
	if target.Ideal != "12Mbps" {
		t.Errorf("matchTarget picked ideal %q, want 12Mbps", target.Ideal)
	}
}

func TestMatchTarget_NoneMatch(t *testing.T) {
	targets := []Target{
		{When: map[string]string{"resolution": "2160p", "video_codec": "hevc"}, Ideal: "45Mbps"},
	}
	if _, ok := matchTarget(targets, FileFacts{Resolution: "720p", VideoCodec: "h264"}); ok {
		t.Error("matchTarget: ok = true for a file matching no target's when clause")
	}
}
