package transcode

import (
	"strings"
)

// MarkerKey is the file tag that says Solarr converted the file.
const MarkerKey = "SOLARR_TRANSCODED"

// keptTags are the tags a conversion writes back, after squeezing (lower case, no separators).
var keptTags = map[string]bool{
	"title": true, "artist": true, "album": true, "albumartist": true, "composer": true, "date": true, "year": true,
	"genre": true, "track": true, "tracknumber": true, "disc": true, "discnumber": true, "totaltracks": true,
	"totaldiscs": true, "compilation": true, "comment": true, "description": true, "synopsis": true, "show": true,
	"seasonnumber": true, "episodeid": true, "episodesort": true, "network": true, "language": true, "lyrics": true,
	"copyright": true, "publisher": true, "label": true, "isrc": true, "barcode": true, "catalognumber": true,
	"media": true, "originaldate": true,
}

// keptPrefixes are the families of tags kept whole.
var keptPrefixes = []string{"musicbrainz", "acoustid", "solarr", "sort"}

func squeezeKey(key string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(key))
}

// KeepTag says whether a file tag survives a conversion; normalising drops ReplayGain, which a loudness change makes false.
func KeepTag(key string, normalising bool) bool {
	name := squeezeKey(key)
	switch {
	case keptTags[name]:
		return true
	case strings.HasPrefix(name, "replaygain"):
		return !normalising
	case strings.HasSuffix(name, "sort"):
		return true
	}
	for _, prefix := range keptPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// MarkerValue is the value of the marker tag: the target, the setup revision, the pass and the day.
func MarkerValue(target string, revision int64, pass int, day string) string {
	return target + ";" + itoa(revision) + ";" + itoa(int64(pass)) + ";" + day
}

// HasMarker says whether the tags carry the marker, whatever the case of its key.
func HasMarker(tags map[string]string) bool {
	for key, value := range tags {
		if strings.EqualFold(key, MarkerKey) && value != "" {
			return true
		}
	}
	return false
}
