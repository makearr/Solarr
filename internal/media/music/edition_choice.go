package music

import (
	"slices"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// chooseRelease picks the edition an album identified only at release-group level takes its
// tracklist from: the best-scoring release (DecideAlbum's order) that at least one file fits.
func chooseRelease(group musicbrainz.ReleaseGroup, releases []musicbrainz.Release, locals []LocalTrack) (musicbrainz.Release, bool) {
	candidates := make([]AlbumCandidate, len(releases))
	for i, release := range releases {
		candidates[i] = AlbumCandidate{ReleaseGroup: group, ArtistCredit: creditNames(group.ArtistCredit), Release: release}
	}
	scores := DecideAlbum(locals, candidates).Scores
	best := slices.IndexFunc(scores, func(score ReleaseScore) bool { return score.Supported > 0 })
	if best < 0 {
		return musicbrainz.Release{}, false
	}
	return scores[best].Candidate.Release, true
}

func trackCount(release musicbrainz.Release) int {
	count := 0
	for _, medium := range release.Media {
		count += len(medium.Tracks)
	}
	return count
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
