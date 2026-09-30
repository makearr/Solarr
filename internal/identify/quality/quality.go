// Package quality judges a file against its library's quality: the target it aims for and the cutoff
// at or above which upgrades stop (Lidarr's cutoff). One judgment for the album page and for marking
// an album cutoff-unmet (M1d).
package quality

import (
	"math"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/score"
)

// File is how one file stands against its library's target and cutoff.
type File struct {
	MeetsTarget bool
	MeetsCutoff bool
}

// Judge judges a file's measured facts against a library's quality; judged is false when the library
// sets neither a target nor a cutoff.
func Judge(snapshot *settings.Snapshot, category settings.Category, facts score.Facts) (file File, judged bool) {
	if category.QualityTarget == nil && category.QualityCutoffID == nil {
		return File{}, false
	}
	if category.QualityTarget != nil {
		file.MeetsTarget = MeetsTarget(*category.QualityTarget, facts.File)
	}
	file.MeetsCutoff = file.MeetsTarget
	if !file.MeetsCutoff && category.QualityCutoffID != nil {
		for _, id := range category.QualityProfileIDs {
			if fits, _, _ := ProfileFits(snapshot, id, facts); fits {
				file.MeetsCutoff = true
				break
			}
			if id == *category.QualityCutoffID {
				break
			}
		}
	}
	return file, true
}

// ProfileFits is score.Fits for a stored profile; known is false when it is missing or its rules do
// not parse.
func ProfileFits(snapshot *settings.Snapshot, profileID int64, facts score.Facts) (fits, known bool, reason string) {
	return judgeProfile(snapshot, profileID, facts, score.Fits)
}

// ProfileAccepts is score.Accepts for a stored profile, with ProfileFits' known.
func ProfileAccepts(snapshot *settings.Snapshot, profileID int64, facts score.Facts) (accepts, known bool, reason string) {
	return judgeProfile(snapshot, profileID, facts, score.Accepts)
}

func judgeProfile(
	snapshot *settings.Snapshot, profileID int64, facts score.Facts, check func(score.Profile, score.Facts) (bool, string),
) (bool, bool, string) {
	profile, found := snapshot.QualityProfile(profileID)
	if !found {
		return false, false, ""
	}
	rules, err := score.Parse([]byte(profile.RulesJSON))
	if err != nil {
		return false, false, ""
	}
	ok, reason := check(rules, facts)
	return ok, true, reason
}

// MeetsTarget is codec and container equal, and the bitrate within the tolerance unless lossless.
func MeetsTarget(target settings.QualityTarget, file score.FileFacts) bool {
	if !strings.EqualFold(target.AudioCodec, file.AudioCodec) || !strings.EqualFold(target.Container, file.Container) {
		return false
	}
	if target.BitrateKbps == nil {
		return true
	}
	want := float64(*target.BitrateKbps)
	got := float64(file.AudioBitrate) / 1000
	return math.Abs(got-want) <= want*float64(target.TolerancePct)/100
}
