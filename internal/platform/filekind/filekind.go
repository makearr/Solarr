// Package filekind says what kind of file a path names, by its extension: the one table every scan, import and query
// asks (revision 2, D3), so "is this video" is never a list of its own anywhere else.
package filekind

import (
	"path"
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
	Other    Kind = "other"
)

var byExtension = map[string]Kind{}

func init() {
	for kind, extensions := range map[Kind][]string{
		Audio:    {".flac", ".mp3", ".m4a", ".aac", ".ogg", ".opus", ".wav", ".aiff", ".aif", ".wma", ".ape", ".wv", ".alac", ".dsf", ".dff"},
		Video:    {".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mov", ".wmv", ".webm", ".mpg", ".mpeg", ".flv", ".ogm", ".iso"},
		Subtitle: {".srt", ".vtt", ".ass", ".ssa", ".sub", ".idx"},
		Image:    {".jpg", ".jpeg", ".png", ".webp", ".bmp", ".gif"},
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
