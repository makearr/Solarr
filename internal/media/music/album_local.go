package music

import (
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// placeholderValues say nothing about a file; a claim holding one abstains.
var placeholderValues = map[string]bool{
	"unknown": true, "unknown artist": true, "unknown album": true, "untitled": true,
	"[unknown]": true, "artist": true, "album": true, "track": true, "various": true, "va": true,
}

// leadingTrackNumber is "03 Title", "03. Title" or "03 - Title" in a file name.
var leadingTrackNumber = regexp.MustCompile(`^(\d{1,3})[\s._-]+(.+)$`)

// ReadLocalTrack is one file as album matching sees it: tags first, then its own name,
// its album folder and the folder above that (ADR-41, the full path is evidence).
func ReadLocalTrack(tags mediatool.Tags, measured mediatool.Measured, relPath string) LocalTrack {
	stemDisc, stemTrack, stemTitle := parseFileName(relPath)
	folder := parse.Parse(albumTitleFallback(relPath)).Identity

	local := LocalTrack{
		Title:         claimFrom(tags.First("title"), stemTitle),
		Artist:        claimFrom(tags.First("albumartist", "album_artist", "artist"), folderArtist(relPath, folder)),
		Album:         claimFrom(tags.First("album"), folder.Album),
		Disc:          firstTrackNumber(tags.First("discnumber", "disc")),
		Track:         firstTrackNumber(tags.First("tracknumber", "track")),
		Year:          yearOf(tags.First("originaldate", "date", "year")),
		Duration:      measured.Duration,
		RecordingMBID: ReadMBIDs(tags).Recording,
	}
	if local.Track == 0 {
		local.Disc, local.Track = stemDisc, stemTrack
	}
	if local.Year == 0 {
		local.Year = folder.Year
	}
	return local
}

// claimFrom prefers the tag, falls back to a parsed value, and marks a value that says nothing.
func claimFrom(tag, parsed string) Claim {
	for _, candidate := range []Claim{{Value: tag, Provenance: ProvenanceTag}, {Value: parsed, Provenance: ProvenanceParsed}} {
		value := strings.TrimSpace(candidate.Value)
		if value == "" {
			continue
		}
		if placeholderValues[strings.ToLower(value)] {
			return Claim{Value: value, Provenance: ProvenancePlaceholder}
		}
		return Claim{Value: value, Provenance: candidate.Provenance}
	}
	return Claim{}
}

// folderArtist is the release folder's own "Artist - Album", else the folder above it.
func folderArtist(relPath string, folder parse.Identity) string {
	if folder.Artist != "" {
		return folder.Artist
	}
	parent := path.Dir(path.Dir(relPath))
	if parent == "." || parent == "/" {
		return ""
	}
	return path.Base(parent)
}

// parseFileName reads "Artist - 03 - Title", "03 - Title" or "103 Title" (disc 1, track 3).
func parseFileName(relPath string) (disc, track int, title string) {
	stem := strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	segments := strings.Split(stem, " - ")
	for index, segment := range segments {
		if number, err := strconv.Atoi(strings.TrimSpace(segment)); err == nil && index < len(segments)-1 {
			disc, track = splitDiscTrack(number)
			return disc, track, strings.Join(segments[index+1:], " - ")
		}
	}
	if found := leadingTrackNumber.FindStringSubmatch(stem); found != nil {
		number, _ := strconv.Atoi(found[1])
		disc, track = splitDiscTrack(number)
		return disc, track, found[2]
	}
	return 0, 0, stem
}

// splitDiscTrack reads a three-digit number as disc and track, the way "103" is disc 1 track 3.
func splitDiscTrack(number int) (disc, track int) {
	if number >= 100 {
		return number / 100, number % 100
	}
	return 0, number
}
