package mediatool_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// framesRunner answers ffprobe with a 320x240 video and ffmpeg with patterned frames of the size its filter asks for.
type framesRunner struct {
	mu     sync.Mutex
	ffmpeg [][]string
	// refuseTogether fails every run that reads several moments, as ffmpeg does when an input cannot be opened.
	refuseTogether bool
}

func (*framesRunner) Lookup(string) error { return nil }
func (*framesRunner) RunInput(context.Context, []byte, string, ...string) ([]byte, error) {
	return nil, nil
}

var sizePattern = regexp.MustCompile(`scale=(\d+):(\d+):flags=area`)

func (f *framesRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "ffmpeg" {
		return []byte(`{"streams":[{"codec_name":"h264","codec_type":"video","width":320,"height":240}],"format":{"format_name":"mov","duration":"600"}}`), nil
	}
	f.mu.Lock()
	f.ffmpeg = append(f.ffmpeg, args)
	f.mu.Unlock()
	joined := strings.Join(args, " ")
	if f.refuseTogether && strings.Contains(joined, "concat=n=") {
		return nil, errors.New("ffmpeg: exit 1")
	}
	match := sizePattern.FindStringSubmatch(joined)
	width, _ := strconv.Atoi(match[1])
	height, _ := strconv.Atoi(match[2])
	frames := 5
	if strings.Contains(joined, "concat=n=") {
		inputs := strings.Count(joined, " -i ")
		frames = 5 * inputs
	}
	out := make([]byte, frames*width*height*3)
	// A smooth gradient with a bright square whose place depends on the call: not flat, not noise.
	shift := len(f.ffmpeg) * 11 % 40
	for y := range height {
		for x := range width {
			value := byte((x*255/width + y*255/height) / 2)
			if x > shift && x < shift+width/3 && y > height/4 && y < height/2 {
				value = 255 - value
			}
			for frame := range frames {
				at := ((frame*height+y)*width + x) * 3
				out[at], out[at+1], out[at+2] = value, value, value
			}
		}
	}
	return out, nil
}

func TestHashAndPreview_ReadsThreeMomentsPerProcess(t *testing.T) {
	runner := &framesRunner{}
	hash, _, err := mediatool.New(runner, 1).HashAndPreview(t.Context(), "/v/a.mkv", false)
	if err != nil {
		t.Fatalf("HashAndPreview: %v", err)
	}
	if len(runner.ffmpeg) != 3 {
		t.Errorf("ffmpeg ran %d times, want 3 (nine moments, three per process)", len(runner.ffmpeg))
	}
	if len(hash.Frames) == 0 {
		t.Error("no frame hashes")
	}
}

func TestHashAndPreview_AMomentGroupFfmpegRefusesIsReadMomentByMoment(t *testing.T) {
	runner := &framesRunner{refuseTogether: true}
	hash, _, err := mediatool.New(runner, 1).HashAndPreview(t.Context(), "/v/a.mkv", false)
	if err != nil || len(hash.Frames) == 0 {
		t.Fatalf("HashAndPreview = %d frames, %v; want the single reads to carry it", len(hash.Frames), err)
	}
	if len(runner.ffmpeg) != 12 {
		t.Errorf("ffmpeg ran %d times, want 12 (three refused groups, then nine single reads)", len(runner.ffmpeg))
	}
}
