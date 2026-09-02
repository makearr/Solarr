// Package music is the music media type, direct-wired until stage 9 extracts it as
// a plugin (ADR-4) once TV has been built alongside it.
package music

import (
	"strings"

	"github.com/makearr/MediaNexus/internal/mediatool"
)

// Provider strings for MusicBrainz identity. They are level-specific because a
// release group MBID and a release MBID are both MusicBrainz UUIDs for different
// things, and ADR-7's work/edition split turns on telling them apart -- ADR-29 is
// blunt that reading only one loses either which album this is or which pressing.
const (
	// ProviderReleaseGroup identifies the work: the album, across every pressing.
	ProviderReleaseGroup = "musicbrainz:release-group"
	// ProviderRelease identifies the edition: one pressing. Confusingly, this is
	// what MusicBrainz tagging calls MUSICBRAINZ_ALBUMID.
	ProviderRelease = "musicbrainz:release"
	// ProviderRecording identifies the part: one recording, which MUSICBRAINZ_TRACKID
	// carries.
	ProviderRecording = "musicbrainz:recording"
	// ProviderArtist identifies a person or group.
	ProviderArtist = "musicbrainz:artist"
)

// mbidLength is a canonical UUID's length, 8-4-4-4-12 with hyphens.
const mbidLength = 36

// MBIDs are the MusicBrainz identities a file carries. Every field is optional: a
// file tagged by hand may have the album and nothing else.
type MBIDs struct {
	ReleaseGroup string
	Release      string
	Recording    string
	// Artists and AlbumArtists keep their order, which is the credit order the
	// tagger recorded (ADR-32 treats people as first-class).
	Artists      []string
	AlbumArtists []string
}

// Empty reports a file with no MusicBrainz identity at all, which is what sends it
// to the next strategy in the chain (ADR-24).
func (m MBIDs) Empty() bool {
	return m.ReleaseGroup == "" && m.Release == "" && m.Recording == "" &&
		len(m.Artists) == 0 && len(m.AlbumArtists) == 0
}

// ReadMBIDs pulls MusicBrainz identity out of a file's tags. **No network call** --
// that is the whole point of ADR-29: the existing library is Lidarr-managed, so the
// files already say what they are.
func ReadMBIDs(tags mediatool.Tags) MBIDs {
	return MBIDs{
		ReleaseGroup: validMBID(tags.First("MUSICBRAINZ_RELEASEGROUPID")),
		Release:      validMBID(tags.First("MUSICBRAINZ_ALBUMID")),
		Recording:    validMBID(tags.First("MUSICBRAINZ_TRACKID")),
		Artists:      validMBIDs(tags.First("MUSICBRAINZ_ARTISTID")),
		AlbumArtists: validMBIDs(tags.First("MUSICBRAINZ_ALBUMARTISTID")),
	}
}

// validMBID drops anything that is not a canonical MBID. A malformed identity is
// worse than none: it would be stored, matched against, and quietly wrong.
func validMBID(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != mbidLength {
		return ""
	}
	for index, r := range trimmed {
		switch index {
		case 8, 13, 18, 23:
			if r != '-' {
				return ""
			}
		default:
			if !isHexDigit(r) {
				return ""
			}
		}
	}
	return strings.ToLower(trimmed)
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// validMBIDs splits a multi-value artist tag. Taggers separate several artists with
// "/" or ";" and ffprobe hands back the joined string, so one credit per id needs
// splitting here rather than by every caller.
func validMBIDs(value string) []string {
	if value == "" {
		return nil
	}
	var found []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '/' || r == ';' || r == ','
	}) {
		if mbid := validMBID(part); mbid != "" {
			found = append(found, mbid)
		}
	}
	return found
}
