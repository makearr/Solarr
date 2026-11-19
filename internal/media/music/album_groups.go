package music

import (
	"cmp"
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// AlbumTags is what one file's tags say about the album it belongs to; Track keeps the raw "N" or "N/total".
type AlbumTags struct {
	Album       string
	AlbumArtist string
	Artist      string
	ReleaseID   string
	Track       string
}

// AlbumTagsOf reads the grouping tags of one file.
func AlbumTagsOf(tags mediatool.Tags) AlbumTags {
	return AlbumTags{
		Album:       strings.TrimSpace(tags.First("album")),
		AlbumArtist: strings.TrimSpace(tags.First("albumartist", "album_artist")),
		Artist:      strings.TrimSpace(tags.First("artist")),
		ReleaseID:   strings.TrimSpace(tags.First("musicbrainz_albumid")),
		Track:       strings.TrimSpace(tags.First("tracknumber", "track")),
	}
}

// trailingDiscRe is the disc marker older rips append to the album tag ("Album (Disc 2)", "Album CD2").
var trailingDiscRe = regexp.MustCompile(`(?i)\s*[\(\[]?\s*\b(?:cd|dis[ck])\s*\d+\s*[\)\]]?$`)

func foldTag(value string) string { return strings.ToLower(strings.Join(strings.Fields(value), " ")) }

// albumTitleKey is the album tag two files must share to be one album: disc marker dropped, case and space folded.
func albumTitleKey(album string) string { return foldTag(trailingDiscRe.ReplaceAllString(album, "")) }

// hasAlbumEvidence reports whether a file's tags name an album at all.
func (t AlbumTags) hasAlbumEvidence() bool { return t.ReleaseID != "" || albumTitleKey(t.Album) != "" }

// albumGroupKey is a group's identity: a release id, or an album title and the artist part splitAlbumByArtist gives.
type albumGroupKey struct{ release, title, artist string }

// GroupByAlbumTags groups files, by index, into the albums their tags name; scan and import share it (MERGE-2).
// Rules and the measurement that chose them: documentation/music-identification.md, "Grouping files into albums".
func GroupByAlbumTags(files []AlbumTags) [][]int {
	keys := make([]albumGroupKey, len(files))
	byTitle := map[string][]int{}
	var titles []string
	for i, file := range files {
		if file.ReleaseID != "" {
			keys[i] = albumGroupKey{release: foldTag(file.ReleaseID)}
			continue
		}
		if title := albumTitleKey(file.Album); title != "" {
			if _, seen := byTitle[title]; !seen {
				titles = append(titles, title)
			}
			byTitle[title] = append(byTitle[title], i)
		}
	}
	for _, title := range titles {
		artists := splitAlbumByArtist(files, byTitle[title])
		for _, i := range byTitle[title] {
			keys[i] = albumGroupKey{title: title, artist: artists[i]}
		}
	}

	var order []albumGroupKey
	groups := map[albumGroupKey][]int{}
	var untagged []int
	for i, key := range keys {
		if key == (albumGroupKey{}) {
			untagged = append(untagged, i)
			continue
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], i)
	}
	order = mergeLooseIntoReleases(files, order, groups)

	result := make([][]int, 0, len(order)+1)
	for _, key := range order {
		result = append(result, groups[key])
	}
	switch {
	case len(untagged) == 0:
	case len(result) == 0:
		result = append(result, untagged)
	case len(result) == 1:
		result[0] = append(result[0], untagged...)
	default:
		result = append(result, untagged)
	}
	return result
}

// mergeLooseIntoReleases moves each title group into the one release its files lost the id of, and answers the
// order without the groups it moved.
func mergeLooseIntoReleases(files []AlbumTags, order []albumGroupKey, groups map[albumGroupKey][]int) []albumGroupKey {
	kept := order[:0:0]
	for _, key := range order {
		if key.release != "" {
			kept = append(kept, key)
			continue
		}
		var home albumGroupKey
		homes := 0
		for _, release := range order {
			if release.release != "" && joinsRelease(files, groups[release], groups[key], key.title) {
				home = release
				homes++
			}
		}
		if homes != 1 {
			kept = append(kept, key)
			continue
		}
		groups[home] = append(groups[home], groups[key]...)
		delete(groups, key)
	}
	return kept
}

// joinsRelease reports whether loose files belong to a release: every release file carries their title and their
// artist is one of the release's. A second copy joins too; album matching places it on the track it copies.
func joinsRelease(files []AlbumTags, release, loose []int, title string) bool {
	artists := map[string]bool{}
	releaseHasAlbumArtist := false
	for _, i := range release {
		if albumTitleKey(files[i].Album) != title {
			return false
		}
		artists[foldTag(cmp.Or(files[i].AlbumArtist, files[i].Artist))] = true
		releaseHasAlbumArtist = releaseHasAlbumArtist || files[i].AlbumArtist != ""
	}
	for _, i := range loose {
		file := files[i]
		if !artists[foldTag(cmp.Or(file.AlbumArtist, file.Artist))] && (file.AlbumArtist != "" || releaseHasAlbumArtist) {
			return false
		}
	}
	return true
}

// splitAlbumByArtist answers, per file index, the artist part of its group key within one album: "" for the whole
// album, or an artist where the files name different album artists, or are singles with no album artist.
func splitAlbumByArtist(files []AlbumTags, members []int) map[int]string {
	keys := make(map[int]string, len(members))
	counts := map[string]int{}
	for _, i := range members {
		if artist := foldTag(files[i].AlbumArtist); artist != "" {
			counts[artist]++
		}
	}
	switch {
	case len(counts) > 1:
		// Two real albums of one title: files without an album artist follow their track artist, else the largest.
		largest := ""
		for artist, count := range counts {
			if count > counts[largest] || (count == counts[largest] && artist < largest) {
				largest = artist
			}
		}
		for _, i := range members {
			artist := foldTag(files[i].AlbumArtist)
			if artist == "" {
				artist = foldTag(files[i].Artist)
				if counts[artist] == 0 {
					artist = largest
				}
			}
			keys[i] = artist
		}
	case len(counts) == 0 && pileOfSingles(files, members):
		for _, i := range members {
			keys[i] = foldTag(files[i].Artist)
		}
	}
	return keys
}

// pileOfSingles is a folder of different artists' tracks under one album word with no album artist and track numbers
// that are absent or mostly collide: singles, not a compilation numbered 1..n.
func pileOfSingles(files []AlbumTags, members []int) bool {
	artists := map[string]bool{}
	numbers := map[string]int{}
	numbered := 0
	for _, i := range members {
		artists[foldTag(files[i].Artist)] = true
		if number := files[i].Track; number != "" {
			numbers[number]++
			numbered++
		}
	}
	if len(artists) < 2 {
		return false
	}
	if numbered == 0 {
		return true
	}
	colliding := 0
	for _, count := range numbers {
		if count > 1 {
			colliding += count
		}
	}
	return colliding*2 > len(members)
}
