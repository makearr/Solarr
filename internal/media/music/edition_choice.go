package music

import (
	"slices"

	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// editionChoice is the release an album takes its tracklist from; Fits is false when no file fits
// any release and the best-ranked one was taken anyway (the user's answer (b), 2026-09-28).
type editionChoice struct {
	Release musicbrainz.Release
	Fits    bool
}

// chooseRelease picks the edition an album identified only at release-group level takes its
// tracklist from: the best-scoring release (DecideAlbum's order) that at least one file fits,
// else the best-scoring one with a tracklist.
func chooseRelease(group musicbrainz.ReleaseGroup, releases []musicbrainz.Release, locals []LocalTrack) (editionChoice, bool) {
	candidates := make([]AlbumCandidate, len(releases))
	for i, release := range releases {
		candidates[i] = AlbumCandidate{ReleaseGroup: group, ArtistCredit: creditNames(group.ArtistCredit), Release: release}
	}
	scores := DecideAlbum(locals, candidates).Scores
	if best := slices.IndexFunc(scores, func(score ReleaseScore) bool { return score.Supported > 0 }); best >= 0 {
		return editionChoice{Release: scores[best].Candidate.Release, Fits: true}, true
	}
	if best := slices.IndexFunc(scores, func(score ReleaseScore) bool { return trackCount(score.Candidate.Release) > 0 }); best >= 0 {
		return editionChoice{Release: scores[best].Candidate.Release}, true
	}
	return editionChoice{}, false
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
