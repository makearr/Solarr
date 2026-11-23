package transcoder

import (
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/transcode"
)

// FileFromMeasured rebuilds a file from what the library already measured: coarse (one real video and audio stream, a
// synthetic one per further language, same codec assumed), enough for every skip rule so a sweep need not probe again.
func FileFromMeasured(measured mediatool.Measured, container string, sizeBytes int64, tags map[string]string) transcode.File {
	file := transcode.File{Container: container, Duration: measured.Duration, SizeBytes: sizeBytes, BitrateBps: measured.OverallBitrate, Tags: tags}
	if measured.VideoCodec != "" {
		file.Streams = append(file.Streams, transcode.Stream{
			Index: len(file.Streams), Type: transcode.StreamVideo, Codec: measured.VideoCodec, BitrateBps: measured.VideoBitrate,
			Width: measured.Width, Height: measured.Height, FrameRate: measured.FrameRate, Interlaced: measured.ScanType == "interlaced",
			HDR: measured.HDR, BitDepth: max(measured.BitDepth, 8),
		})
	}
	audio := measured.AudioLanguages
	if len(audio) == 0 && measured.AudioCodec != "" {
		audio = []string{""}
	}
	for position, language := range audio {
		stream := transcode.Stream{
			Index: len(file.Streams), Type: transcode.StreamAudio, Codec: measured.AudioCodec, Language: language,
			Channels: channelCount(measured.ChannelLayout, measured.Channels), SampleRate: measured.SampleRate,
		}
		if position == 0 {
			stream.BitrateBps = measured.AudioBitrate
		}
		file.Streams = append(file.Streams, stream)
	}
	for _, language := range measured.SubtitleLanguages {
		file.Streams = append(file.Streams, transcode.Stream{Index: len(file.Streams), Type: transcode.StreamSubtitle, Codec: "subrip", Language: language})
	}
	return file
}
