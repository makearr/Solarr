package music

import (
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

// chooseRelease picks the edition an album identified only at release-group
// level should take its tracklist from: the release its files fit best.
// Official releases are preferred when there are any. Among them, the most
// files aligned wins; then the track count closest to the files present, so a
// plain album is not read as its deluxe edition; then the earliest dated.
// False when no release aligns a single file.
func chooseRelease(releases []musicbrainz.Release, files []TrackFile) (musicbrainz.Release, bool) {
	candidates := officialOrAll(releases)

	var best musicbrainz.Release
	bestMatched, bestDistance, found := 0, 0, false
	for _, release := range candidates {
		matched := alignedFiles(release, files)
		if matched == 0 {
			continue
		}
		distance := abs(trackCount(release) - len(files))
		better := !found ||
			matched > bestMatched ||
			(matched == bestMatched && distance < bestDistance) ||
			(matched == bestMatched && distance == bestDistance && earlierDate(release.Date, best.Date))
		if better {
			best, bestMatched, bestDistance, found = release, matched, distance, true
		}
	}
	return best, found
}

func officialOrAll(releases []musicbrainz.Release) []musicbrainz.Release {
	var official []musicbrainz.Release
	for _, release := range releases {
		if release.Status == "official" {
			official = append(official, release)
		}
	}
	if len(official) == 0 {
		return releases
	}
	return official
}

// alignedFiles is how many files Align places on this release's tracklist.
func alignedFiles(release musicbrainz.Release, files []TrackFile) int {
	var parts []Part
	for _, medium := range release.Media {
		for _, track := range medium.Tracks {
			parts = append(parts, Part{
				MediaRef: mediaRef(medium.Position, track.Position), Title: track.Title,
				RecordingMBID: track.RecordingMBID,
			})
		}
	}
	matched := 0
	for _, part := range Align(parts, files).Parts {
		if part.File != nil {
			matched++
		}
	}
	return matched
}

func trackCount(release musicbrainz.Release) int {
	count := 0
	for _, medium := range release.Media {
		count += len(medium.Tracks)
	}
	return count
}

// earlierDate orders MusicBrainz partial dates as strings, which sort
// chronologically; an undated release never counts as earlier.
func earlierDate(candidate, current string) bool {
	return candidate != "" && (current == "" || candidate < current)
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
