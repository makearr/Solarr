package works_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

func TestRecogniseVideoRelease_ListsOnlyWhatTheTitleStates(t *testing.T) {
	got := works.RecogniseVideoRelease("Invented.Show.S01E02.1080p.WEB-DL.DDP5.1.H.264-GRP")
	want := []media.ReleaseClaim{
		{Key: "resolution", Value: "1080p"}, {Key: "source", Value: "webdl"}, {Key: "video_codec", Value: "h264"},
		{Key: "audio_codec", Value: "eac3"}, {Key: "channels", Value: "5.1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claims = %+v, want %+v", got, want)
	}
	if got := works.RecogniseVideoRelease("Invented Show"); len(got) != 0 {
		t.Errorf("a title with no claims listed %+v", got)
	}
}
