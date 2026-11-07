package transcode

import (
	"context"
	"encoding/json"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
)

// SidecarBase numbers a subtitle file beside the video after the tracks inside it, so one id names either.
const SidecarBase = 1000

// sidecarExtensions are the subtitle files read beside a video; ffmpeg converts each to WebVTT.
var sidecarExtensions = []string{".srt", ".vtt", ".ass", ".ssa"}

// bitmapSubtitleCodecs are pictures, not text, so they cannot become WebVTT.
var bitmapSubtitleCodecs = []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub", "dvb_teletext"}

// SubtitleTrack is one subtitle a viewer can show: a stream inside the video, or a file beside it.
type SubtitleTrack struct {
	// ID is the stream's place among the video's subtitle streams, or SidecarBase plus the file's place among its sidecars.
	ID       int
	Language string
	Title    string
	Codec    string
	Default  bool
}

// SubtitleTracks lists the text subtitles of the video at filePath: those inside it, then the files beside it.
func (s *Service) SubtitleTracks(ctx context.Context, filePath string) ([]SubtitleTrack, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := s.runner.Run(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "file", "-select_streams", "s", "-show_entries",
		"stream=codec_name:stream_tags=language,title:stream_disposition=default", "-of", "json", filePath)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Streams []struct {
			CodecName   string            `json:"codec_name"`
			Tags        map[string]string `json:"tags"`
			Disposition struct {
				Default int `json:"default"`
			} `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	var tracks []SubtitleTrack
	for ordinal, stream := range parsed.Streams {
		if slices.Contains(bitmapSubtitleCodecs, stream.CodecName) {
			continue
		}
		tracks = append(tracks, SubtitleTrack{
			ID: ordinal, Language: stream.Tags["language"], Title: stream.Tags["title"], Codec: stream.CodecName, Default: stream.Disposition.Default == 1,
		})
	}
	for place, name := range s.sidecars(filePath) {
		stem := strings.TrimSuffix(name, path.Ext(name))
		tracks = append(tracks, SubtitleTrack{ID: SidecarBase + place, Language: sidecarLanguage(stem, filePath), Title: name, Codec: strings.TrimPrefix(path.Ext(name), ".")})
	}
	return tracks, nil
}

// sidecars are the subtitle files in the video's folder that start with the video's own name, in name order.
func (s *Service) sidecars(filePath string) []string {
	entries, err := s.fsys.ReadDir(path.Dir(filePath))
	if err != nil {
		return nil
	}
	stem := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	var names []string
	for _, entry := range entries {
		if !entry.IsDir && strings.HasPrefix(entry.Name, stem) && slices.Contains(sidecarExtensions, strings.ToLower(path.Ext(entry.Name))) {
			names = append(names, entry.Name)
		}
	}
	slices.Sort(names)
	return names
}

// sidecarLanguage is the code between the video's name and the extension in "Film.en.srt"; empty when there is none.
func sidecarLanguage(subtitleStem, videoPath string) string {
	videoStem := strings.TrimSuffix(path.Base(videoPath), path.Ext(videoPath))
	rest := strings.Trim(strings.TrimPrefix(subtitleStem, videoStem), ".")
	if i := strings.LastIndex(rest, "."); i >= 0 {
		rest = rest[i+1:]
	}
	if len(rest) >= 2 && len(rest) <= 3 {
		return rest
	}
	return ""
}

// SubtitleVTT writes track id of the video at filePath as WebVTT, its times moved back by startSecond so they line up
// with a conversion that began there.
func (s *Service) SubtitleVTT(ctx context.Context, w io.Writer, filePath string, id int, startSecond float64) error {
	if !s.Available() {
		return ErrUnavailable
	}
	source, mapping := filePath, []string{"-map", "0:s:" + strconv.Itoa(id)}
	if id >= SidecarBase {
		names := s.sidecars(filePath)
		if id-SidecarBase >= len(names) {
			return ErrNoSuchTrack
		}
		source, mapping = path.Join(path.Dir(filePath), names[id-SidecarBase]), nil
	}
	args := []string{"-v", "error"}
	if startSecond > 0 {
		args = append(args, "-ss", strconv.FormatFloat(startSecond, 'f', 3, 64))
	}
	args = append(args, "-protocol_whitelist", "file", "-i", source)
	args = append(args, mapping...)
	args = append(args, "-f", "webvtt", "-")
	return s.piper.Pipe(ctx, w, ffmpeg, args...)
}
