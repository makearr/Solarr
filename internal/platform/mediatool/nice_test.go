package mediatool

import (
	"reflect"
	"testing"
)

func TestNiceWrap_ZeroValueLeavesTheCommandAlone(t *testing.T) {
	name, args := Nice{}.Wrap("ffmpeg", []string{"-i", "a.mkv"})
	if name != "ffmpeg" || !reflect.DeepEqual(args, []string{"-i", "a.mkv"}) {
		t.Fatalf("got %q %v, want the command unchanged", name, args)
	}
}

func TestNiceWrap_PutsTheLevelBeforeTheCommand(t *testing.T) {
	niceOnce.Do(func() {})
	nicePath = "/usr/bin/nice"
	name, args := Nice{Level: 19}.Wrap("ffmpeg", []string{"-i", "a.mkv"})
	if name != "/usr/bin/nice" || !reflect.DeepEqual(args, []string{"-n", "19", "ffmpeg", "-i", "a.mkv"}) {
		t.Fatalf("got %q %v", name, args)
	}
	nicePath = ""
	if name, _ := (Nice{Level: 19}).Wrap("ffmpeg", nil); name != "ffmpeg" {
		t.Fatalf("without the tool the command must run as it is, got %q", name)
	}
}
