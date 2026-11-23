package transcode

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func parseGolden(t *testing.T, name string) File {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "streams_"+name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := parseStreams(raw)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestParseStreams_MatroskaWithTwoAudioLanguagesPictureAndTextSubtitlesAFontAndChapters(t *testing.T) {
	file := parseGolden(t, "mkv")
	if file.Container != "matroska,webm" || file.Duration != 5400500*time.Millisecond || file.SizeBytes != 2000000000 || file.BitrateBps != 2962000 || file.Chapters != 3 || file.Tags["title"] != "Harbour Lights" {
		t.Errorf("file = %+v", file)
	}
	want := []Stream{
		{Index: 0, Type: StreamVideo, Codec: "h264", Language: "en", BitrateBps: 2400000, Default: true, Width: 1920, Height: 800, FrameRate: 24000.0 / 1001, BitDepth: 8},
		{Index: 1, Type: StreamAudio, Codec: "ac3", Language: "en", Title: "Surround", BitrateBps: 448000, Default: true, Channels: 6, SampleRate: 48000},
		{Index: 2, Type: StreamAudio, Codec: "aac", Language: "de", BitrateBps: 128000, Channels: 2, SampleRate: 44100},
		{Index: 3, Type: StreamAudio, Codec: "aac", Language: "en", Title: "Director Commentary", BitrateBps: 96000, Commentary: true, Channels: 2, SampleRate: 44100},
		{Index: 4, Type: StreamSubtitle, Codec: "hdmv_pgs_subtitle", Language: "en", Forced: true, Picture: true},
		{Index: 5, Type: StreamSubtitle, Codec: "subrip", HearingImpaired: true},
		{Index: 6, Type: StreamAttachment, Codec: "ttf"},
	}
	if !reflect.DeepEqual(file.Streams, want) {
		t.Errorf("streams = %+v\nwant     %+v", file.Streams, want)
	}
}

func TestParseStreams_ACoverPictureIsNotVideoAndAnUntaggedStreamHasNoLanguage(t *testing.T) {
	file := parseGolden(t, "m4a")
	if got := file.Streams[0]; got.Language != "" || got.Type != StreamAudio {
		t.Errorf("audio = %+v", got)
	}
	if got := file.Streams[1]; !got.AttachedPicture || got.Type != StreamVideo || got.FrameRate != 0 {
		t.Errorf("cover = %+v", got)
	}
}

func TestParseStreams_DataStreamsAreTypedData(t *testing.T) {
	file := parseGolden(t, "mp4data")
	if file.Streams[2].Type != StreamData || file.Streams[3].Type != StreamData || file.Streams[0].FrameRate != 30 {
		t.Errorf("streams = %+v", file.Streams)
	}
}

func TestParseStreams_InterlacedBitDepthAndHDRReadingsComeFromTheStream(t *testing.T) {
	if got := parseGolden(t, "interlaced").Streams[0]; !got.Interlaced || got.FrameRate != 25 {
		t.Errorf("interlaced = %+v", got)
	}
	if got := parseGolden(t, "mkv").Streams[0]; got.Interlaced {
		t.Errorf("progressive read as interlaced: %+v", got)
	}
	if got := parseGolden(t, "hdr10").Streams[0]; got.HDR != "hdr10" || got.BitDepth != 10 || got.DolbyVisionProfile != 0 {
		t.Errorf("hdr10 = %+v", got)
	}
	if got := parseGolden(t, "dolbyvision").Streams[0]; got.HDR != "dolby-vision" || got.DolbyVisionProfile != 5 || got.BitDepth != 10 {
		t.Errorf("dolby vision = %+v", got)
	}
}

func TestParseStreams_RefusesDocumentsThatAreNotJSON(t *testing.T) {
	if _, err := parseStreams([]byte("not json")); err == nil {
		t.Error("garbage accepted")
	}
}
