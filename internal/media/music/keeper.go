package music

import (
	"fmt"
	"path"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
)

// KeeperFile is one stored copy of a track, as far as its quality goes.
type KeeperFile struct {
	AudioCodec  string
	Container   string
	Lossless    bool
	BitrateKbps int64
	BitDepth    int64
	SampleRate  int64
	// Path names the file; its ending is the extension a profile may rank.
	Path string
}

func (f KeeperFile) facts() score.Facts {
	return score.Facts{File: score.FileFacts{
		AudioCodec: f.AudioCodec, Extension: strings.TrimPrefix(strings.ToLower(path.Ext(f.Path)), "."), Container: f.Container,
		Lossless: f.Lossless, AudioBitrate: f.BitrateKbps * 1000, OverallBitrate: f.BitrateKbps * 1000,
		BitDepth: int(f.BitDepth), SampleRate: int(f.SampleRate),
	}}
}

// ChooseKeeper picks which of several copies of one track a library should keep: the one that meets the library's
// target quality, else the one its profiles rank highest, else the better measured one. It answers the index and
// why, in words a person can check.
func ChooseKeeper(snapshot *settings.Snapshot, categoryID int64, files []KeeperFile) (keeper int, reason string) {
	if len(files) == 0 {
		return -1, ""
	}
	category, found := snapshot.Category(categoryID)
	meets := make([]bool, len(files))
	judged := false
	if found {
		for i, file := range files {
			if judgement, ok := quality.Judge(snapshot, category, file.facts()); ok {
				judged = true
				meets[i] = judgement.MeetsTarget
			}
		}
	}
	candidates := make([]int, 0, len(files))
	for i := range files {
		if meets[i] {
			candidates = append(candidates, i)
		}
	}
	targetNote := ""
	if len(candidates) > 0 && len(candidates) < len(files) {
		targetNote = "it meets the library's target quality and the others do not"
	}
	if len(candidates) == 0 {
		for i := range files {
			candidates = append(candidates, i)
		}
		if judged {
			targetNote = "none meets the library's target, so the one its profiles rank highest"
		}
	}
	profiles := categoryRankedProfiles(snapshot, categoryID)
	best := candidates[0]
	if len(profiles) > 0 {
		for _, i := range candidates[1:] {
			if judge.StrictlyBetter(judge.EvaluateInOrder(profiles, files[i].facts(), nil), judge.EvaluateInOrder(profiles, files[best].facts(), nil)) {
				best = i
			}
		}
		if targetNote == "" {
			targetNote = "its profiles rank it highest"
		}
	}
	for _, i := range candidates {
		if i != best && len(profiles) == 0 && betterMeasured(files[i], files[best]) {
			best = i
		}
	}
	if targetNote == "" {
		targetNote = "it is the better measured file"
	}
	return best, fmt.Sprintf("%s (%s)", targetNote, describeFile(files[best]))
}

func categoryRankedProfiles(snapshot *settings.Snapshot, categoryID int64) []judge.RankedProfile {
	category, found := snapshot.Category(categoryID)
	if !found {
		return nil
	}
	var profiles []judge.RankedProfile
	for _, id := range category.QualityProfileIDs {
		profile, found := snapshot.QualityProfile(id)
		if !found {
			continue
		}
		rules, err := score.Parse([]byte(profile.RulesJSON))
		if err != nil {
			continue
		}
		profiles = append(profiles, judge.RankedProfile{Name: profile.Name, Rules: rules})
	}
	return profiles
}

// betterMeasured is the fallback with no profile to judge by: lossless over lossy, then the higher bitrate.
func betterMeasured(a, b KeeperFile) bool {
	if a.Lossless != b.Lossless {
		return a.Lossless
	}
	return a.BitrateKbps > b.BitrateKbps
}

func describeFile(f KeeperFile) string {
	codec := f.AudioCodec
	if codec == "" {
		codec = strings.TrimPrefix(path.Ext(f.Path), ".")
	}
	if f.Lossless || f.BitrateKbps == 0 {
		return strings.ToUpper(codec)
	}
	return fmt.Sprintf("%s %d kbit", strings.ToUpper(codec), f.BitrateKbps)
}
