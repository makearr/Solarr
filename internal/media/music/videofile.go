package music

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/filekind"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// Subtypes of a video work: the work.subtype of a music video or concert film, beside MusicBrainz's release-group types.
const (
	SubtypeConcertVideo = "Concert video"
	SubtypeMusicVideo   = "Music video"
)

// VideoSubtypes are the subtypes of every video work, as the Albums and Videos levels tell them apart.
func VideoSubtypes() []string { return []string{SubtypeConcertVideo, SubtypeMusicVideo} }

// Library kinds that fix what a video is (kinds.go); any other music library decides by running time.
const (
	kindConcertVideos = "concert_videos"
	kindMusicVideos   = "music_videos"
)

// ProviderLocalVideo is the identity of a video known only by its file name (D6's local identity, for video).
const ProviderLocalVideo = "local:video"

// concertLength is the running time from which a video with no stated kind is a concert, not a clip.
const concertLength = 25 * time.Minute

// IsMusicFile is the scan's Include for music libraries: sound, and the video features beside it.
func IsMusicFile(relPath string) bool {
	return filekind.Of(relPath) == filekind.Audio || filekind.IsFeature(relPath)
}

// IsVideoFile says a music library's file is a video work rather than a track.
func IsVideoFile(relPath string) bool { return filekind.Of(relPath) == filekind.Video }

// VideoClaim is what a video file says about itself before any lookup: who made it, what it is called, when.
type VideoClaim struct {
	Artist  string
	Title   string
	Year    int
	Subtype string
}

var (
	videoYear    = regexp.MustCompile(`[\[(]\s*((?:19|20)\d{2})(?:[-.]\d{2}[-.]\d{2})?\s*[\])]|\b((?:19|20)\d{2})[-.]\d{2}[-.]\d{2}\b`)
	videoBracket = regexp.MustCompile(`\s*[\[(][^\])]*[\])]`)
	videoNoise   = regexp.MustCompile(`(?i)[ ._-]+(?:2160p|1080[pi]|720p|480p|web-?dl|webrip|blu-?ray|bdrip|hdtv|dvdrip|x26[45]|h\.?26[45]|hevc|aac|ac3)\b.*$`)
)

// InspectVideo reads a video file's own claims: tags first, then "Artist - Title (year)" in the name, the parent
// folder naming the artist when the name does not. Probe facts decide the subtype when the library does not.
func InspectVideo(relPath string, probe mediatool.Probe, libraryKind string) VideoClaim {
	name := strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	claim := VideoClaim{Artist: probe.Tags.First("artist", "albumartist", "album_artist"), Title: probe.Tags.First("title")}
	if match := videoYear.FindStringSubmatch(name); match != nil {
		claim.Year, _ = strconv.Atoi(match[1] + match[2])
	}
	if claim.Year == 0 {
		claim.Year = leadingYear(probe.Tags.First("date", "year", "originaldate"))
	}
	name = strings.TrimSpace(videoNoise.ReplaceAllString(name, ""))
	name = strings.TrimSpace(strings.NewReplacer("_", " ").Replace(videoBracket.ReplaceAllString(name, "")))
	nameArtist, nameTitle := "", name
	if artist, title, split := strings.Cut(name, " - "); split {
		nameArtist, nameTitle = strings.TrimSpace(artist), strings.TrimSpace(title)
	}
	if nameArtist == "" {
		if parent := path.Base(path.Dir(relPath)); parent != "." && parent != "/" {
			nameArtist = parent
		}
	}
	if claim.Artist == "" {
		claim.Artist = nameArtist
	}
	if claim.Title == "" {
		claim.Title = nameTitle
	}
	claim.Subtype = videoSubtype(libraryKind, probe.Measured.Duration)
	return claim
}

func leadingYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return year
}

func videoSubtype(libraryKind string, duration time.Duration) string {
	switch {
	case libraryKind == kindConcertVideos:
		return SubtypeConcertVideo
	case libraryKind == kindMusicVideos:
		return SubtypeMusicVideo
	case duration >= concertLength:
		return SubtypeConcertVideo
	default:
		return SubtypeMusicVideo
	}
}

// WantedVideosOf names the video works the wanted lists include: those of every music library that switched videos on.
func WantedVideosOf(snapshot *settings.Snapshot) library.WantedVideos {
	var videos library.WantedVideos
	for _, category := range snapshot.Categories() {
		if category.MediaType == "music" && category.MusicVideos {
			videos.CategoryIDs = append(videos.CategoryIDs, category.ID)
		}
	}
	if len(videos.CategoryIDs) > 0 {
		videos.Subtypes = VideoSubtypes()
	}
	return videos
}
