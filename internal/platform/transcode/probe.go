package transcode

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
)

const (
	ffprobe      = "ffprobe"
	probeTimeout = 8 * time.Second
)

// Source is what ffprobe reads of one file, for a file the library has not measured yet.
type Source struct {
	Width, Height          int
	VideoCodec, AudioCodec string
	OverallKbps, VideoKbps int64
	DurationMs             int64
}

// ProbeSource asks ffprobe for the size, codecs, bitrates and length of path.
func (s *Service) ProbeSource(ctx context.Context, path string) (Source, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := s.runner.Run(ctx, ffprobe, "-v", "error", "-show_entries",
		"format=bit_rate,duration:stream=codec_type,codec_name,width,height,bit_rate", "-of", "json", path)
	if err != nil {
		return Source{}, err
	}
	var parsed struct {
		Format struct {
			BitRate  string `json:"bit_rate"`
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			BitRate   string `json:"bit_rate"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return Source{}, err
	}
	source := Source{OverallKbps: kbps(parsed.Format.BitRate)}
	if seconds, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
		source.DurationMs = int64(seconds * 1000)
	}
	for _, stream := range parsed.Streams {
		switch stream.CodecType {
		case "video":
			if source.VideoCodec == "" {
				source.Width, source.Height, source.VideoCodec, source.VideoKbps = stream.Width, stream.Height, stream.CodecName, kbps(stream.BitRate)
			}
		case "audio":
			if source.AudioCodec == "" {
				source.AudioCodec = stream.CodecName
			}
		}
	}
	return source, nil
}

func kbps(bitsPerSecond string) int64 {
	value, err := strconv.ParseInt(bitsPerSecond, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value / 1000
}
