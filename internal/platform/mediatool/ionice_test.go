package mediatool

import (
	"reflect"
	"testing"
)

func TestIONiceWrap_ZeroValueLeavesTheCommandAlone(t *testing.T) {
	name, args := IONice{}.Wrap("ffprobe", []string{"-i", "a.mkv"})
	if name != "ffprobe" || !reflect.DeepEqual(args, []string{"-i", "a.mkv"}) {
		t.Fatalf("got %q %v, want the command unchanged", name, args)
	}
}

func TestIONiceWrap_PutsTheClassAndLevelBeforeTheCommand(t *testing.T) {
	ioniceOnce.Do(func() {})
	ionicePath = "/usr/bin/ionice"
	name, args := IONice{Class: 2, Level: 7}.Wrap("ffprobe", []string{"-i", "a.mkv"})
	want := []string{"-c", "2", "-n", "7", "ffprobe", "-i", "a.mkv"}
	if name != "/usr/bin/ionice" || !reflect.DeepEqual(args, want) {
		t.Fatalf("got %q %v, want ionice %v", name, args, want)
	}
	_, args = IONice{Class: 3}.Wrap("ffmpeg", nil)
	if !reflect.DeepEqual(args, []string{"-c", "3", "ffmpeg"}) {
		t.Fatalf("idle class args = %v, want -c 3 ffmpeg", args)
	}
}
