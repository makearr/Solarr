package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

func TestOSHash(t *testing.T) {
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	const block = 65536
	zeros := make([]byte, 2*block)
	writeFile(t, fsys, "/media/zeros.mkv", zeros)
	headWord := make([]byte, 2*block)
	headWord[0] = 1
	writeFile(t, fsys, "/media/head.mkv", headWord)
	tailWord := make([]byte, 2*block)
	tailWord[2*block-8] = 2
	writeFile(t, fsys, "/media/tail.mkv", tailWord)
	middle := make([]byte, 3*block)
	middle[block+8] = 9
	writeFile(t, fsys, "/media/middle.mkv", middle)
	writeFile(t, fsys, "/media/small.mkv", make([]byte, 100))
	writeFile(t, fsys, "/media/song.flac", zeros)

	cases := []struct {
		path string
		size int64
		want string
	}{
		{"/media/zeros.mkv", 2 * block, "0000000000020000"},
		{"/media/head.mkv", 2 * block, "0000000000020001"},
		{"/media/tail.mkv", 2 * block, "0000000000020002"},
		{"/media/middle.mkv", 3 * block, "0000000000030000"},
		{"/media/small.mkv", 100, ""},
		{"/media/song.flac", 2 * block, ""},
	}
	for _, c := range cases {
		got, err := library.OSHash(fsys, c.path, c.size)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.path, got, err, c.want)
		}
	}
}
