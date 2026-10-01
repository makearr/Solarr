package score

import "strings"

// Facts is what Evaluate reads about one candidate, split into ReleaseFacts (known
// before download) and FileFacts (known only after a probe, ADR-39).
type Facts struct {
	Release ReleaseFacts
	File    FileFacts
}

// ReleaseFacts are claims about the candidate itself -- title, group, swarm health
// -- not about the media the release contains.
type ReleaseFacts struct {
	Title        string
	ReleaseGroup string
	Seeders      int
	Leechers     int
	Protocol     string // "usenet" | "torrent" | "ddl"
}

// FileFacts mirrors library.db's media_attributes table (measured half, ADR-39) --
// generic across media types, so a video and a music profile read different subsets.
type FileFacts struct {
	Resolution        string // "2160p" | "1080p" | "720p" | ...
	Source            string // "remux" | "bluray" | "webdl" | ...
	VideoCodec        string
	HDR               string
	AudioCodec        string
	AudioChannels     string // "7.1" | "5.1" | "2.0"
	BitDepth          int
	AudioLanguages    []string
	SubtitleLanguages []string
	EditionTokens     []string
	ReleaseGroup      string
	ProperRepack      bool
	VideoBitrate      int64 // bits/sec
	AudioBitrate      int64
	OverallBitrate    int64
	DurationMs        int64
	Width             int
	Height            int
	FrameRate         float64
	ScanType          string // "progressive" | "interlaced"
	// Container is ffprobe's own answer; Extension the file's name ending. Either alone stands for the container.
	Container  string
	Extension  string
	Lossless   bool
	SampleRate int
}

// stringField resolves a scoring spec's field name to a string. File-level facts
// win over release-level ones for fields both could carry (release_group, ADR-39).
func (facts Facts) stringField(field string) (string, bool) {
	switch field {
	case "title":
		return facts.Release.Title, facts.Release.Title != ""
	case "protocol":
		return facts.Release.Protocol, facts.Release.Protocol != ""
	case "release_group":
		if facts.File.ReleaseGroup != "" {
			return facts.File.ReleaseGroup, true
		}
		return facts.Release.ReleaseGroup, facts.Release.ReleaseGroup != ""
	case "resolution":
		return facts.File.Resolution, facts.File.Resolution != ""
	case "source":
		return facts.File.Source, facts.File.Source != ""
	case "video_codec":
		return facts.File.VideoCodec, facts.File.VideoCodec != ""
	case "hdr":
		return facts.File.HDR, facts.File.HDR != ""
	case "audio_codec":
		return facts.File.AudioCodec, facts.File.AudioCodec != ""
	case "audio_channels":
		return facts.File.AudioChannels, facts.File.AudioChannels != ""
	case "scan_type":
		return facts.File.ScanType, facts.File.ScanType != ""
	case "container":
		name := facts.File.Container
		if name == "" {
			name = facts.File.Extension
		}
		return CanonicalContainer(name), name != ""
	case "extension":
		extension := strings.ToLower(strings.TrimPrefix(facts.File.Extension, "."))
		return extension, extension != ""
	default:
		return "", false
	}
}

// listField resolves a scoring spec's field name to a list value, for the "has"
// operator.
func (facts Facts) listField(field string) ([]string, bool) {
	switch field {
	case "edition_tokens":
		if len(facts.File.EditionTokens) > 0 {
			return facts.File.EditionTokens, true
		}
		return nil, false
	case "audio_languages":
		return facts.File.AudioLanguages, len(facts.File.AudioLanguages) > 0
	case "subtitle_languages":
		return facts.File.SubtitleLanguages, len(facts.File.SubtitleLanguages) > 0
	default:
		return nil, false
	}
}

// numberField resolves a scoring spec's field name to a numeric value, for
// gte/gt/lte/lt.
func (facts Facts) numberField(field string) (float64, bool) {
	switch field {
	case "seeders":
		return float64(facts.Release.Seeders), true
	case "leechers":
		return float64(facts.Release.Leechers), true
	case "bit_depth":
		return float64(facts.File.BitDepth), facts.File.BitDepth != 0
	case "sample_rate":
		return float64(facts.File.SampleRate), facts.File.SampleRate != 0
	case "frame_rate":
		return facts.File.FrameRate, facts.File.FrameRate != 0
	case "video_bitrate":
		return float64(facts.File.VideoBitrate), facts.File.VideoBitrate != 0
	case "audio_bitrate":
		return float64(facts.File.AudioBitrate), facts.File.AudioBitrate != 0
	case "overall_bitrate":
		return float64(facts.File.OverallBitrate), facts.File.OverallBitrate != 0
	default:
		return 0, false
	}
}

func containsFold(list []string, target string) bool {
	for _, item := range list {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
