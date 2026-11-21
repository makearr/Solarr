package music

import (
	"cmp"
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

// shortName is a Windows 8.3 name ("DORA9E~V") that replaced the real one somewhere along a copy.
var shortName = regexp.MustCompile(`^[A-Z0-9_$]{1,7}~[A-Z0-9]{1,7}$`)

// leadingTrackNumber is "03 Title", "03. Title" or "03 - Title" in a file name.
var leadingTrackNumber = regexp.MustCompile(`^(\d{1,3})[\s._-]+(.+)$`)

// ReadLocalTrack is one file as album matching sees it: tags first, then its own name,
// its album folder and the folder above that (ADR-41, the full path is evidence).
func ReadLocalTrack(tags mediatool.Tags, measured mediatool.Measured, relPath string) LocalTrack {
	stemArtist, stemDisc, stemTrack, stemTitle := parseFileName(relPath)
	folder := parse.Parse(albumTitleFallback(relPath)).Identity

	local := LocalTrack{
		Title:         claimFrom(tags.First("title"), stemTitle),
		Artist:        claimFrom(tags.First("albumartist", "album_artist", "artist"), stemArtist, folderArtist(relPath, folder)),
		TrackArtist:   claimFrom(tags.First("artist")),
		Album:         claimFrom(tags.First("album"), folder.Album),
		Disc:          firstTrackNumber(tags.First("discnumber", "disc")),
		Track:         firstTrackNumber(tags.First("tracknumber", "track")),
		Year:          yearOf(tags.First("date", "year")),
		OriginalYear:  yearOf(tags.First("originaldate", "originalyear")),
		Duration:      measured.Duration,
		RecordingMBID: ReadMBIDs(tags).Recording,
		FolderAlbum:   folder.Album,
		Barcode:       barcodeDigits(tags.First("barcode", "upc")),
		CatalogNumber: strings.TrimSpace(tags.First("catalognumber", "catalog", "labelno")),
		TrackTotal:    trackTotal(tags.First("tracknumber", "track"), tags.First("tracktotal", "totaltracks")),
	}
	if local.Track == 0 {
		local.Disc, local.Track = stemDisc, stemTrack
	}
	// With only one of the two dates tagged there is no telling which it is, so both fields take it.
	switch {
	case local.Year == 0:
		local.Year = cmp.Or(local.OriginalYear, folder.Year)
		local.OriginalYear = cmp.Or(local.OriginalYear, local.Year)
	case local.OriginalYear == 0:
		local.OriginalYear = local.Year
	}
	return local
}

// barcodeDigits is a barcode tag's digits when they make a plausible code (8 to 14), else empty.
func barcodeDigits(value string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, value)
	if len(digits) < 8 || len(digits) > 14 {
		return ""
	}
	return digits
}

// barcodeKey is a barcode without its leading zeros, so a UPC and the same code as an EAN compare equal.
func barcodeKey(value string) string {
	return strings.TrimLeft(barcodeDigits(value), "0")
}

// claimFrom prefers the tag, then each parsed value in turn; a value that says nothing is passed
// over, and answered as a placeholder only when nothing else says more.
func claimFrom(tag string, parsed ...string) Claim {
	var placeholder Claim
	for i, value := range append([]string{tag}, parsed...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if saysNothing(value) {
			if placeholder.Value == "" {
				placeholder = Claim{Value: value, Provenance: ProvenancePlaceholder}
			}
			continue
		}
		if i == 0 {
			return Claim{Value: value, Provenance: ProvenanceTag}
		}
		return Claim{Value: value, Provenance: ProvenanceParsed}
	}
	return placeholder
}

// saysNothing is a placeholder or a mangled short name.
func saysNothing(value string) bool {
	return placeholderValues[strings.ToLower(value)] || shortName.MatchString(value)
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
func parseFileName(relPath string) (artist string, disc, track int, title string) {
	stem := strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	segments := strings.Split(stem, " - ")
	for index, segment := range segments {
		if number, err := strconv.Atoi(strings.TrimSpace(segment)); err == nil && index < len(segments)-1 {
			disc, track = splitDiscTrack(number)
			return strings.Join(segments[:index], " - "), disc, track, strings.Join(segments[index+1:], " - ")
		}
	}
	if found := leadingTrackNumber.FindStringSubmatch(stem); found != nil {
		number, _ := strconv.Atoi(found[1])
		disc, track = splitDiscTrack(number)
		return "", disc, track, found[2]
	}
	return "", 0, 0, stem
}

// splitDiscTrack reads a three-digit number as disc and track, the way "103" is disc 1 track 3.
func splitDiscTrack(number int) (disc, track int) {
	if number >= 100 {
		return number / 100, number % 100
	}
	return 0, number
}
