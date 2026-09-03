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

// TestBitrateDistance_PenalizesBothDirections is ADR-40's worked example: against
// a 4 Mbit/s ideal, overshooting to 10 Mbit/s costs more than undershooting to 3.
func TestBitrateDistance_PenalizesBothDirections(t *testing.T) {
	target := BitrateTarget{Bitrate: BitrateRange{Ideal: "4Mbps"}}
	weights := BitrateWeights{Over: 1, Under: 1}

	over, ok := bitrateDistance(10_000_000, target, weights, true)
	if !ok {
		t.Fatal("bitrateDistance(10Mbps): ok = false")
	}
	under, ok := bitrateDistance(3_000_000, target, weights, true)
	if !ok {
		t.Fatal("bitrateDistance(3Mbps): ok = false")
	}

	if diff := over - 1.321928; diff > 0.001 || diff < -0.001 {
		t.Errorf("bitrateDistance(10Mbps) = %v, want ~1.321928", over)
	}
	if diff := under - 0.415037; diff > 0.001 || diff < -0.001 {
		t.Errorf("bitrateDistance(3Mbps) = %v, want ~0.415037", under)
	}
	if over <= under {
		t.Errorf("bitrateDistance(10Mbps)=%v should exceed bitrateDistance(3Mbps)=%v", over, under)
	}
}

func TestBitrateDistance_WeightsScaleTheAsymmetry(t *testing.T) {
	target := BitrateTarget{Bitrate: BitrateRange{Ideal: "4Mbps"}}
	weights := BitrateWeights{Over: 2, Under: 1}

	over, _ := bitrateDistance(8_000_000, target, weights, true)
	if diff := over - 2.0; diff > 0.001 || diff < -0.001 {
		t.Errorf("weighted overshoot distance = %v, want ~2.0 (raw 1.0 * weight 2)", over)
	}
}

func TestBitrateDistance_NoTargetIsNotOK(t *testing.T) {
	if _, ok := bitrateDistance(5_000_000, BitrateTarget{}, BitrateWeights{Over: 1, Under: 1}, false); ok {
		t.Error("bitrateDistance with hasTarget=false: ok = true, want false")
	}
}

func TestBitrateDistance_UnmeasuredRateIsNotOK(t *testing.T) {
	target := BitrateTarget{Bitrate: BitrateRange{Ideal: "4Mbps"}}
	if _, ok := bitrateDistance(0, target, BitrateWeights{Over: 1, Under: 1}, true); ok {
		t.Error("bitrateDistance with 0 bits/sec: ok = true, want false")
	}
}

func TestMatchTarget_FirstMatchWins(t *testing.T) {
	targets := []BitrateTarget{
		{When: map[string]string{"resolution": "2160p", "video_codec": "hevc"}, Bitrate: BitrateRange{Ideal: "45Mbps"}},
		{When: map[string]string{"resolution": "1080p", "video_codec": "h264"}, Bitrate: BitrateRange{Ideal: "12Mbps"}},
	}
	target, ok := matchTarget(targets, FileFacts{Resolution: "1080p", VideoCodec: "h264"})
	if !ok {
		t.Fatal("matchTarget: ok = false, want true")
	}
	if target.Bitrate.Ideal != "12Mbps" {
		t.Errorf("matchTarget picked ideal %q, want 12Mbps", target.Bitrate.Ideal)
	}
}

func TestMatchTarget_NoneMatch(t *testing.T) {
	targets := []BitrateTarget{
		{When: map[string]string{"resolution": "2160p", "video_codec": "hevc"}, Bitrate: BitrateRange{Ideal: "45Mbps"}},
	}
	if _, ok := matchTarget(targets, FileFacts{Resolution: "720p", VideoCodec: "h264"}); ok {
		t.Error("matchTarget: ok = true for a file matching no target's when clause")
	}
}
