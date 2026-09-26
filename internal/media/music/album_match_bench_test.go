package music_test

import (
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
)

// A popular album: one 30-track tracklist pressed a hundred times.
func BenchmarkDecideAlbum_ManyPressingsOfOneTracklist(b *testing.B) {
	var tracks []trackSpec
	for i := range 30 {
		tracks = append(tracks, trackSpec{fmt.Sprintf("Track %d", i+1), 180 + i*7})
	}
	var candidates []music.AlbumCandidate
	for i := range 100 {
		candidates = append(candidates, candidateOf("rg", "Album", nil, releaseOf(fmt.Sprintf("rel-%d", i), "Kebilegev Bibihi", "official", "2017", tracks)))
	}
	locals := folderOf(tracks)
	for b.Loop() {
		music.DecideAlbum(locals, candidates)
	}
}
