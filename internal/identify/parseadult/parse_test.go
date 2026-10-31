package parseadult_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/identify/parseadult"
)

func TestParsePathReadsASceneBelowItsStudioFolder(t *testing.T) {
	for _, test := range []struct {
		path string
		want parseadult.Info
	}{
		{"Night Studio/Class Two - Scene 3 (2020-05-14)/[Night Studio] Class Two - Scene 3 (2020-05-14) .mp4",
			parseadult.Info{Studio: "Night Studio", Title: "Class Two - Scene 3", Date: "2020-05-14", Year: 2020}},
		{"Big Label/Wet Weekend(2018)/[] Wet Weekend (2018).mp4", parseadult.Info{Studio: "Big Label", Title: "Wet Weekend", Year: 2018}},
		{"Plain Film(2007)/Plain Film (2007).mp4", parseadult.Info{Title: "Plain Film", Year: 2007}},
		{"Night Studio/Two - Scene 1 (2025-04-17)/[Night Studio] Two - Scene 1 (2025-04-17) - Ada Vale.mp4",
			parseadult.Info{Studio: "Night Studio", Title: "Two - Scene 1", Date: "2025-04-17", Year: 2025, Performer: "Ada Vale"}},
	} {
		got, ok := parseadult.ParsePath(test.path)
		if !ok || !reflect.DeepEqual(got, test.want) {
			t.Errorf("ParsePath(%q) = %+v, %v; want %+v", test.path, got, ok, test.want)
		}
	}
}

func TestParseReleaseReadsTheGroupsDottedName(t *testing.T) {
	got, ok := parseadult.ParseRelease("NightStudio.20.05.14.Ada.Vale.Class.Two.XXX.1080p.MP4-GRP")
	want := parseadult.Info{Studio: "NightStudio", Date: "2020-05-14", Year: 2020, Title: "Ada Vale Class Two", Words: "Ada Vale Class Two"}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, %v; want %+v", got, ok, want)
	}
	if _, ok := parseadult.ParseRelease("Some.Movie.2020.1080p"); ok {
		t.Error("a name with no day is no scene release")
	}
}
