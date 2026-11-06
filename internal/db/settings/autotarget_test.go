package settings_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func TestAutoTarget_IsTheAverageOfWhatTheProfileRanksFirst(t *testing.T) {
	video := settings.QualityProfile{Name: "Mine", RulesJSON: `{"tiers":{"resolution":[["1080p"],["720p"]],"video_codec":[["h265","av1"],["h264"]],"audio_channels":[["5.1"],["2.0"]],
		"container":[["matroska"],["mp4"]]},"targets":{"video_bitrate":[{"min":"2000kbps","max":"6000kbps"}],"audio_bitrate":[{"ideal":"384kbps"}],"frame_rate":[{"ideal":"24"}]}}`}
	got, ok := settings.AutoTarget(video, "movie")
	if !ok {
		t.Fatal("a profile that ranks resolution and codec gave no target")
	}
	rate, audio := int64(4000), int64(384)
	want := settings.QualityTarget{
		Name: "Automatic: Mine", MediaType: "video", Label: "Automatic", Description: got.Description, Resolution: "1080p", VideoCodec: "h265", AudioChannels: "5.1",
		Container: "mkv", VideoBitrateKbps: &rate, AudioBitrateKbps: &audio, FrameRate: 24,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("video target = %+v, want %+v", got, want)
	}

	music := settings.QualityProfile{Name: "Lossy", RulesJSON: `{"tiers":{"audio_codec":[["aac"],["mp3"]]},"targets":{"bitrate":[
		{"when":{"audio_codec":"mp3"},"min":"192kbps","ideal":"320kbps"},{"when":{"audio_codec":"aac"},"min":"160kbps","ideal":"256kbps","max":"320kbps"}],
		"sample_rate":[{"min":"44100","ideal":"48kHz"}]}}`}
	audioTarget, ok := settings.AutoTarget(music, "music")
	if !ok || audioTarget.AudioCodec != "aac" || audioTarget.Container != "m4a" || audioTarget.BitrateKbps == nil || *audioTarget.BitrateKbps != 256 || audioTarget.SampleRateHz != 48000 {
		t.Errorf("music target = %+v (ok %v), want aac in m4a at 256 kbit/s and 48 kHz", audioTarget, ok)
	}
	lossless := settings.QualityProfile{Name: "Lossless", RulesJSON: `{"tiers":{"audio_codec":[["flac","alac"],["mp3"]]},"targets":{"bitrate":[{"ideal":"320kbps"}]}}`}
	if target, _ := settings.AutoTarget(lossless, "music"); target.AudioCodec != "flac" || target.BitrateKbps != nil || target.Container != "flac" {
		t.Errorf("a lossless first tier has no bitrate: %+v", target)
	}
	if _, ok := settings.AutoTarget(settings.QualityProfile{Name: "Empty", RulesJSON: `{"compare":["score"]}`}, "movie"); ok {
		t.Error("a profile that says nothing gave a target")
	}
}

func TestSetCategoryTargetAuto_FollowsTheFirstProfileAndYieldsToAPickedTarget(t *testing.T) {
	cache, _ := newCache(t)
	ids := shippedProfileIDs(t, cache)
	category, err := cache.CreateCategory(t.Context(), "music", "Songs", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryQuality(t.Context(), category.ID, []int64{ids["Space conscious"]}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryTargetAuto(t.Context(), category.ID, true); err != nil {
		t.Fatal(err)
	}
	read, _ := cache.Snapshot().Category(category.ID)
	if !read.QualityTargetAuto || read.QualityTarget == nil || read.QualityTarget.AudioCodec != "opus" {
		t.Fatalf("automatic target = %+v (auto %v), want Space conscious' first codec, opus", read.QualityTarget, read.QualityTargetAuto)
	}
	if err := cache.SetCategoryQuality(t.Context(), category.ID, []int64{ids["Lossy 320"]}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if read, _ = cache.Snapshot().Category(category.ID); read.QualityTargetAuto || read.QualityTarget != nil {
		t.Errorf("saving the quality without the automatic flag left it on: %+v", read.QualityTarget)
	}
}
