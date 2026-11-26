package mediatool_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestDecodeTailDecodesOnlyTheLastSecondsOfALongFileAndReportsAFailure(t *testing.T) {
	var args []string
	runner := &fakeRunner{
		respond: func(string, string) ([]byte, error) {
			return nil, errors.New("Invalid data found when processing input")
		},
		record: func(got []string) { args = slices.Clone(got) },
	}
	tool := mediatool.New(runner, 1)
	err := tool.DecodeTail(t.Context(), "/library/Lantern/film.mkv", 90*time.Minute)
	if err == nil {
		t.Fatal("DecodeTail = nil, want the failure")
	}
	if want := []string{"-v", "error", "-xerror", "-nostdin", "-ss", "5392.0", "-i", "/library/Lantern/film.mkv", "-t", "8", "-f", "null", "-"}; !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
	runner.calls = 0
	if err := tool.DecodeTail(t.Context(), "/library/Lantern/short.mkv", 10*time.Second); err != nil || runner.calls != 0 {
		t.Errorf("a file under half a minute: err = %v, calls = %d, want neither", err, runner.calls)
	}
}
