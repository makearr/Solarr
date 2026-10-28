// Package filekind says what kind of file a path names, by its extension: the one table every scan, import and query
// asks (revision 2, D3), so "is this video" is never a list of its own anywhere else.
package filekind

import (
	"path"
	"regexp"
	"strings"
)

// Kind is what a file holds.
type Kind string

// The kinds.
const (
	Audio    Kind = "audio"
	Video    Kind = "video"
	Subtitle Kind = "subtitle"
	Image    Kind = "image"
	// Book is an ebook: media to a book library, a booklet beside an album to the rest.
	Book  Kind = "book"
	Other Kind = "other"
)

var byExtension = map[string]Kind{}

func init() {
	for kind, extensions := range map[Kind][]string{
		Audio:    {".flac", ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".aiff", ".aif", ".wma", ".ape", ".wv", ".alac", ".dsf", ".dff"},
		Video:    {".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mov", ".wmv", ".webm", ".mpg", ".mpeg", ".flv", ".ogm", ".iso"},
		Subtitle: {".srt", ".vtt", ".ass", ".ssa", ".sub", ".idx"},
		Image:    {".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif"},
		Book:     {".epub", ".azw3", ".azw", ".kfx", ".mobi", ".pdf", ".fb2", ".djvu", ".lit", ".cbz", ".cbr", ".pdb", ".pml"},
	} {
		for _, extension := range extensions {
			byExtension[extension] = kind
		}
	}
}

// Of answers the kind of the file relPath names; Other for any extension the table does not know.
func Of(relPath string) Kind {
	if kind, known := byExtension[strings.ToLower(path.Ext(relPath))]; known {
		return kind
	}
	return Other
}

// extraFolders are the folder names Sonarr, Radarr, Plex and Jellyfin keep non-feature video in.
var extraFolders = map[string]bool{
	"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true, "interviews": true,
	"scenes": true, "shorts": true, "trailers": true, "other": true, "sample": true, "samples": true,
}

var sampleName = regexp.MustCompile(`(?:^|[-._ \[(])sample(?:$|[-._ \])])`)

// IsFeature reports a video file that is the thing itself, not a sample, trailer or extra beside it: the files a TV or
// movie scan probes.
func IsFeature(relPath string) bool {
	if Of(relPath) != Video {
		return false
	}
	components := strings.Split(strings.ToLower(relPath), "/")
	for _, folder := range components[:len(components)-1] {
		if extraFolders[folder] {
			return false
		}
	}
	file := components[len(components)-1]
	return !sampleName.MatchString(strings.TrimSuffix(file, path.Ext(file)))
}
