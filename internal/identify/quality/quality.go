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
	// Target is how near the file is to the library's target; nil when it has none or nothing could be compared.
	Target *Closeness
}

// Judge judges a file's measured facts against a library's quality; judged is false when the library
// sets neither a target nor a cutoff.
func Judge(snapshot *settings.Snapshot, category settings.Category, facts score.Facts) (file File, judged bool) {
	if category.QualityTarget == nil && category.QualityCutoffID == nil {
		return File{}, false
	}
	if category.QualityTarget != nil {
		file.MeetsTarget = MeetsTarget(*category.QualityTarget, facts.File)
		if closeness, ok := Distance(*category.QualityTarget, facts.File); ok {
			file.Target = &closeness
		}
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
	if target.IsVideo() {
		return meetsVideoTarget(target, file)
	}
	// The target's file type is an extension ("m4a"), met by the same container as well as the same name.
	name := file.Extension
	if name == "" {
		name = file.Container
	}
	if !strings.EqualFold(target.AudioCodec, file.AudioCodec) ||
		score.CanonicalContainer(target.Container) != score.CanonicalContainer(name) {
		return false
	}
	if target.AudioChannels != "" && file.AudioChannels != "" && target.AudioChannels != file.AudioChannels {
		return false
	}
	// A file at the sample rate and depth or above meets it, as a transcode only lowers them; 44.1 kHz meets 48 kHz,
	// which is what a transcode of a CD rip leaves.
	if target.SampleRateHz > 0 && file.SampleRate > 0 && float64(file.SampleRate) < float64(target.SampleRateHz)*sampleRateFloor {
		return false
	}
	if target.BitDepth > 0 && file.BitDepth > 0 && int64(file.BitDepth) < target.BitDepth {
		return false
	}
	if target.BitrateKbps == nil {
		return true
	}
	// A file at or above the bitrate meets it (a transcode only brings it down), and one a little under it is the
	// bitrate a variable-rate encode lands near; only a file far under is short of it.
	want := float64(*target.BitrateKbps)
	got := float64(file.AudioBitrate) / 1000
	return got >= want*math.Min(audioBitrateFloor, 1-float64(target.TolerancePct)/100)
}

// frameRateMargin is how far a frame rate may be from the target's: 23.976 is 24 and 29.97 is 30.
const frameRateMargin = 0.02

// crfMargin is how far above a target CRF a file may be and still meet it: transcodes of one library land at CRF 26 to 30
// by content and tool, and two points is hardly visible, so a file within it is the quality the target asks for.
const crfMargin = 2.0

// sampleRateFloor is the share of a target's sample rate below which a file is short of it.
const sampleRateFloor = 0.9

// videoBitrateFloor and audioBitrateFloor are the share of a target's bitrate below which a file is short of it: a file
// already transcoded to the target's container, codecs and frame rate lands near its bitrate, not on it, and a constant
// quality encode of a quiet film is far under it, so a bitrate alone rarely says a file is not the target.
const (
	videoBitrateFloor = 0.4
	audioBitrateFloor = 0.7
)

// defaultBitrateTolerancePct is the margin of a video target that names none; pictures vary far more than sound does.
const defaultBitrateTolerancePct = 25

// meetsVideoTarget is every part the target names equal to what the probe measured; a part it leaves empty is free,
// and a part the probe could not read (no language tag, no frame rate) does not count against the file.
func meetsVideoTarget(target settings.QualityTarget, file score.FileFacts) bool {
	if target.Resolution != "" && !strings.EqualFold(target.Resolution, file.Resolution) {
		return false
	}
	if target.VideoCodec != "" && !strings.EqualFold(target.VideoCodec, file.VideoCodec) {
		return false
	}
	if target.HDR != "" {
		have := file.HDR
		if have == "" {
			have = "sdr"
		}
		if !strings.EqualFold(target.HDR, have) {
			return false
		}
	}
	if target.AudioCodec != "" && !strings.EqualFold(target.AudioCodec, file.AudioCodec) {
		return false
	}
	if target.AudioChannels != "" && file.AudioChannels != "" && target.AudioChannels != file.AudioChannels {
		return false
	}
	if !anyContainer(target.Container, file) || !shares(target.AudioLanguages, file.AudioLanguages) || !shares(target.SubtitleLanguages, file.SubtitleLanguages) {
		return false
	}
	if !atLeastShare(target.VideoBitrateKbps, file.VideoBitrate, videoBitrateFloor) || !atLeastShare(target.AudioBitrateKbps, file.AudioBitrate, audioBitrateFloor) {
		return false
	}
	if target.CRF > 0 && file.CRF > 0 && file.CRF > target.CRF+crfMargin {
		return false
	}
	if target.MaxFrameRate > 0 && file.FrameRate > target.MaxFrameRate*(1+frameRateMargin) {
		return false
	}
	if target.FrameRate > 0 && file.FrameRate > 0 && math.Abs(file.FrameRate-target.FrameRate) > target.FrameRate*frameRateMargin {
		return false
	}
	return true
}

// anyContainer is the file's container among the target's file types (comma separated), or any when none is named.
func anyContainer(want string, file score.FileFacts) bool {
	if strings.TrimSpace(want) == "" {
		return true
	}
	name := file.Extension
	if name == "" {
		name = file.Container
	}
	for _, option := range strings.Split(want, ",") {
		if score.CanonicalContainer(option) == score.CanonicalContainer(name) {
			return true
		}
	}
	return false
}

// shares is at least one wanted language among the file's, or true when none is wanted or the file states none.
func shares(want, have []string) bool {
	if len(want) == 0 || len(have) == 0 {
		return true
	}
	for _, language := range want {
		for _, present := range have {
			if strings.EqualFold(language, present) {
				return true
			}
		}
	}
	return false
}

// atLeastShare is the measured bits per second at least floor of the target kbit/s, above it as much as you like; true when
// no target is named or nothing was measured.
func atLeastShare(target *int64, measuredBps int64, floor float64) bool {
	if target == nil || measuredBps <= 0 {
		return true
	}
	return float64(measuredBps)/1000 >= float64(*target)*floor
}
