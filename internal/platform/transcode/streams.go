package transcode

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

const streamsProbeTimeout = 60 * time.Second

// StreamType is what a stream carries.
type StreamType string

// The stream types; anything ffprobe cannot name is unknown.
const (
	StreamVideo      StreamType = "video"
	StreamAudio      StreamType = "audio"
	StreamSubtitle   StreamType = "subtitle"
	StreamAttachment StreamType = "attachment"
	StreamData       StreamType = "data"
	StreamUnknown    StreamType = "unknown"
)

// File is everything ffprobe says a file holds.
type File struct {
	Container  string
	Duration   time.Duration
	SizeBytes  int64
	BitrateBps int64
	Chapters   int
	Tags       map[string]string
	Streams    []Stream
}

// Stream is one stream of a file.
type Stream struct {
	Index int
	Type  StreamType
	Codec string
	// Language is a two-letter code when one is known, else what the file states; "" is untagged.
	Language string
	Title    string
	// BitrateBps is the stream's bit_rate, else the BPS tag, else 0.
	BitrateBps                                                    int64
	Default, Forced, Commentary, HearingImpaired, AttachedPicture bool

	Width, Height      int
	FrameRate          float64
	Interlaced         bool
	BitDepth           int
	HDR                string
	DolbyVisionProfile int

	Channels   int
	SampleRate int

	// Picture is a subtitle drawn as an image, which cannot be converted to text.
	Picture bool
}

// ProbeStreams reads every stream, the chapters and the tags of a file.
func (s *Service) ProbeStreams(ctx context.Context, path string) (File, error) {
	ctx, cancel := context.WithTimeout(ctx, streamsProbeTimeout)
	defer cancel()
	output, err := s.runner.Run(ctx, ffprobe, "-v", "error", "-protocol_whitelist", "file", "-show_format", "-show_streams", "-show_chapters", "-of", "json", path)
	if err != nil {
		return File{}, err
	}
	return parseStreams(output)
}

type probedStream struct {
	Index         int               `json:"index"`
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	AvgFrameRate  string            `json:"avg_frame_rate"`
	RFrameRate    string            `json:"r_frame_rate"`
	FieldOrder    string            `json:"field_order"`
	PixFmt        string            `json:"pix_fmt"`
	BitsPerRaw    string            `json:"bits_per_raw_sample"`
	ColorTransfer string            `json:"color_transfer"`
	Channels      int               `json:"channels"`
	SampleRate    string            `json:"sample_rate"`
	BitRate       string            `json:"bit_rate"`
	NbFrames      string            `json:"nb_frames"`
	Tags          map[string]string `json:"tags"`
	Disposition   map[string]int    `json:"disposition"`
	SideDataList  []struct {
		SideDataType string `json:"side_data_type"`
		DVProfile    int    `json:"dv_profile"`
	} `json:"side_data_list"`
}

// parseStreams reads ffprobe's JSON; it is separate from the call so tests feed documents.
func parseStreams(output []byte) (File, error) {
	var parsed struct {
		Format struct {
			FormatName string            `json:"format_name"`
			Duration   string            `json:"duration"`
			Size       string            `json:"size"`
			BitRate    string            `json:"bit_rate"`
			Tags       map[string]string `json:"tags"`
		} `json:"format"`
		Streams  []probedStream `json:"streams"`
		Chapters []struct{}     `json:"chapters"`
	}
	if err := json.Unmarshal(output, &parsed); err != nil {
		return File{}, fmt.Errorf("transcode: read ffprobe output: %w", err)
	}
	file := File{
		Container: parsed.Format.FormatName, SizeBytes: parseNumber(parsed.Format.Size), BitrateBps: parseNumber(parsed.Format.BitRate),
		Chapters: len(parsed.Chapters), Tags: lowerKeys(parsed.Format.Tags),
	}
	if seconds, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
		file.Duration = time.Duration(seconds * float64(time.Second))
	}
	for _, raw := range parsed.Streams {
		file.Streams = append(file.Streams, streamOf(raw))
	}
	return file, nil
}

func parseNumber(text string) int64 {
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func lowerKeys(tags map[string]string) map[string]string {
	out := make(map[string]string, len(tags))
	for key, value := range tags {
		out[strings.ToLower(key)] = value
	}
	return out
}

// pictureSubtitles are the subtitle codecs that draw an image.
var pictureSubtitles = map[string]bool{"hdmv_pgs_subtitle": true, "dvd_subtitle": true, "dvb_subtitle": true, "xsub": true}

func streamOf(raw probedStream) Stream {
	tags := lowerKeys(raw.Tags)
	stream := Stream{
		Index: raw.Index, Codec: raw.CodecName, Language: mediatool.LanguageCode(tags["language"]), Title: tags["title"],
		Default: raw.Disposition["default"] == 1, Forced: raw.Disposition["forced"] == 1, HearingImpaired: raw.Disposition["hearing_impaired"] == 1,
		BitrateBps: parseNumber(raw.BitRate),
	}
	if stream.BitrateBps == 0 {
		for _, key := range []string{"bps", "bps-eng", "bps-" + tags["language"]} {
			if value := parseNumber(tags[key]); value > 0 {
				stream.BitrateBps = value
				break
			}
		}
	}
	stream.Commentary = raw.Disposition["comment"] == 1 || strings.Contains(strings.ToLower(stream.Title), "commentary")
	switch raw.CodecType {
	case "video":
		stream.Type = StreamVideo
		stream.AttachedPicture = raw.Disposition["attached_pic"] == 1 || (isPictureCodec(raw.CodecName) && raw.NbFrames == "1")
		stream.Width, stream.Height = raw.Width, raw.Height
		if !stream.AttachedPicture {
			stream.FrameRate = rate(raw.AvgFrameRate, raw.RFrameRate)
		}
		stream.Interlaced = raw.FieldOrder != "" && raw.FieldOrder != "progressive" && raw.FieldOrder != "unknown"
		stream.BitDepth = bitDepth(raw.BitsPerRaw, raw.PixFmt)
		stream.HDR = mediatool.HDRFormat(raw.ColorTransfer, func(kind string) bool {
			for _, side := range raw.SideDataList {
				if strings.Contains(strings.ToLower(side.SideDataType), kind) {
					return true
				}
			}
			return false
		})
		for _, side := range raw.SideDataList {
			if side.DVProfile > 0 {
				stream.DolbyVisionProfile = side.DVProfile
			}
		}
	case "audio":
		stream.Type = StreamAudio
		stream.Channels, stream.SampleRate = raw.Channels, int(parseNumber(raw.SampleRate))
	case "subtitle":
		stream.Type = StreamSubtitle
		stream.Picture = pictureSubtitles[raw.CodecName]
	case "attachment":
		stream.Type = StreamAttachment
	case "data":
		stream.Type = StreamData
	default:
		stream.Type = StreamUnknown
	}
	return stream
}

func isPictureCodec(codec string) bool { return codec == "mjpeg" || codec == "png" || codec == "bmp" }

// rate is frames a second from ffprobe's fraction, the average first, then the nominal one.
func rate(average, nominal string) float64 {
	for _, text := range []string{average, nominal} {
		numerator, denominator, found := strings.Cut(text, "/")
		if !found {
			continue
		}
		top, topErr := strconv.ParseFloat(numerator, 64)
		bottom, bottomErr := strconv.ParseFloat(denominator, 64)
		if topErr == nil && bottomErr == nil && bottom > 0 && top > 0 {
			return top / bottom
		}
	}
	return 0
}

// bitDepth is the stated depth, else what the pixel format implies.
func bitDepth(stated, pixelFormat string) int {
	if depth, err := strconv.Atoi(stated); err == nil && depth > 0 {
		return depth
	}
	switch {
	case strings.Contains(pixelFormat, "12le"), strings.Contains(pixelFormat, "12be"):
		return 12
	case strings.Contains(pixelFormat, "10le"), strings.Contains(pixelFormat, "10be"), strings.HasPrefix(pixelFormat, "p010"):
		return 10
	}
	return 8
}
