package judge_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
)

func upgradeProfiles(target settings.QualityTarget) []judge.RankedProfile {
	rules := score.Profile{Compare: []string{"score"}}
	return []judge.RankedProfile{{Name: "p", Rules: judge.WithFreshTarget(rules, target), UpgradeRules: judge.WithTarget(rules, target), Target: &target}}
}

func TestUpgradeBeats_AFileAlreadyOnTargetIsNeverReplacedByABitrate(t *testing.T) {
	bitrate := int64(1600)
	target := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", Container: "mp4", VideoBitrateKbps: &bitrate}
	profiles := judge.ForUpgrade(upgradeProfiles(target))
	evaluate := func(kbps int64, codec string) (score.Facts, score.FileFacts) {
		file := score.FileFacts{Resolution: "1080p", VideoCodec: codec, Extension: "mp4", VideoBitrate: kbps * 1000}
		return score.Facts{File: file}, file
	}
	owned, ownedFile := evaluate(900, "h265")
	download, _ := evaluate(1600, "h265")
	if judge.UpgradeBeats(profiles, judge.EvaluateInOrder(profiles, download, nil), judge.EvaluateInOrder(profiles, owned, nil), ownedFile, -1) {
		t.Error("a 900 kbit/s h265 mp4 file is a transcode landing near the target; a 1600 download must not replace it")
	}
	bigger, _ := evaluate(6000, "h265")
	if judge.UpgradeBeats(profiles, judge.EvaluateInOrder(profiles, bigger, nil), judge.EvaluateInOrder(profiles, owned, nil), ownedFile, -1) {
		t.Error("a much larger download must not replace a file that is already on target")
	}
}

func TestUpgradeBeats_AFileOffTheTargetNeedsAClearlyNearerNumber(t *testing.T) {
	bitrate := int64(1600)
	target := settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", Container: "mp4", VideoBitrateKbps: &bitrate}
	profiles := judge.ForUpgrade(upgradeProfiles(target))
	facts := func(kbps int64) (score.Facts, score.FileFacts) {
		file := score.FileFacts{Resolution: "1080p", VideoCodec: "h264", Extension: "mkv", VideoBitrate: kbps * 1000}
		return score.Facts{File: file}, file
	}
	owned, ownedFile := facts(1000)
	slightly, _ := facts(1250)
	clearly, _ := facts(1600)
	if judge.UpgradeBeats(profiles, judge.EvaluateInOrder(profiles, slightly, nil), judge.EvaluateInOrder(profiles, owned, nil), ownedFile, -1) {
		t.Error("a slightly nearer bitrate replaced a file that is off target")
	}
	far, farFile := facts(300)
	if !judge.UpgradeBeats(profiles, judge.EvaluateInOrder(profiles, clearly, nil), judge.EvaluateInOrder(profiles, far, nil), farFile, -1) {
		t.Error("a bitrate five times nearer did not replace a starved file")
	}
}

func TestTargetOnly_RanksByTheTargetAndRefusesOnlyOldCodecs(t *testing.T) {
	bitrate := int64(1600)
	profiles := []judge.RankedProfile{judge.TargetOnly(settings.QualityTarget{MediaType: "movie", Resolution: "1080p", VideoCodec: "h265", VideoBitrateKbps: &bitrate})}
	facts := func(resolution, codec, source string, kbps int64) score.Facts {
		return score.Facts{File: score.FileFacts{Resolution: resolution, VideoCodec: codec, Source: source, VideoBitrate: kbps * 1000}}
	}
	best := judge.EvaluateInOrder(profiles, facts("1080p", "h265", "webdl", 1600), nil)
	if best.Outcome != model.OutcomeAccept {
		t.Fatalf("a release matching the target was %v", best.Outcome)
	}
	for name, worse := range map[string]score.Facts{
		"another codec":    facts("1080p", "h264", "webdl", 1600),
		"a lower picture":  facts("720p", "h265", "webdl", 1600),
		"a worse source":   facts("1080p", "h265", "hdtv", 1600),
		"a far-off number": facts("1080p", "h265", "webdl", 300),
	} {
		decision := judge.EvaluateInOrder(profiles, worse, nil)
		if decision.Outcome != model.OutcomeAccept {
			t.Errorf("%s was refused; only old codecs are", name)
		}
		if winner, _ := model.Compare(best.Quality, decision.Quality); winner != -1 {
			t.Errorf("%s ranked with or above the release that matches the target", name)
		}
	}
	if old := judge.EvaluateInOrder(profiles, facts("1080p", "xvid", "webdl", 1600), nil); old.Outcome != model.OutcomeReject {
		t.Errorf("xvid = %v, want rejected", old.Outcome)
	}
}
