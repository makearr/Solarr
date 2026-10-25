package book

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/parsebook"
)

// Format is what one of the two media types is: its key and words, the files it holds and where releases of it are
// listed. Everything else is the same for both.
type Format struct {
	MediaType string
	Label     string
	One, Many string
	// Extensions are the files a library of it holds, best first where nothing else ranks them.
	Extensions []string
	// ByFolder says a book is its folder (an audiobook's chapters), not one file named after it.
	ByFolder bool
	// FeedCategories are Newznab's categories the format's releases are listed in.
	FeedCategories []int
	// Sources are the providers a library of it asks, in its order, unless it chose its own.
	Sources []string
	// SizeFloorBytes is the least a download holding one may weigh.
	SizeFloorBytes int64
}

// Newznab's categories: Books/Ebook, and Audio/Audiobook.
const (
	newznabEbook     = 7020
	newznabBooks     = 7000
	newznabAudiobook = 3030
)

// Ebook is the books media type, Readarr's ebook library.
var Ebook = Format{
	MediaType: "book", Label: "Books", One: "Book", Many: "Books",
	Extensions:     []string{"epub", "azw3", "azw", "kfx", "mobi", "pdf", "fb2", "djvu", "txt", "rtf", "lit", "docx"},
	FeedCategories: []int{newznabEbook, newznabBooks},
	Sources:        settings.DefaultBookSources,
	// An ebook can be a few hundred kilobytes; a placeholder is less.
	SizeFloorBytes: 20 << 10,
}

// Audiobook is the audiobooks media type, Readarr's audiobook library and Audiobookshelf's.
var Audiobook = Format{
	MediaType: "audiobook", Label: "Audiobooks", One: "Audiobook", Many: "Audiobooks",
	Extensions:     []string{"m4b", "m4a", "mp3", "aac", "flac", "ogg", "opus", "wma", "mp4"},
	ByFolder:       true,
	FeedCategories: []int{newznabAudiobook},
	Sources:        settings.DefaultAudiobookSources,
	SizeFloorBytes: 1 << 20,
}

// Formats are both, books first.
var Formats = []Format{Ebook, Audiobook}

// IsAudio says the format's files are sound.
func (f Format) IsAudio() bool { return f.ByFolder }

// Includes says a library file is one of the format's.
func (f Format) Includes(relPath string) bool {
	extension := strings.ToLower(strings.TrimPrefix(path.Ext(relPath), "."))
	if !slices.Contains(f.Extensions, extension) {
		return false
	}
	base := strings.ToLower(path.Base(relPath))
	return !strings.HasPrefix(base, "._") && !samplePattern.MatchString(strings.TrimSuffix(base, path.Ext(base)))
}

// samplePattern is a file named a sample, not one whose title has the word in it ("Samples of Life").
var samplePattern = regexp.MustCompile(`(?:^|[-._ \[(])sample(?:$|[-._ \])])`)

// Parse reads what a library file's path says of its book.
func (f Format) Parse(relPath string) (parsebook.Info, bool) {
	return parsebook.ParsePath(relPath, f.ByFolder)
}

// Claims says a release name claims a file of the format: an ebook release naming only audio formats is an audiobook.
func (f Format) Claims(info parsebook.Info) bool {
	if len(info.Formats) == 0 {
		return true
	}
	return slices.ContainsFunc(info.Formats, func(format string) bool { return slices.Contains(f.Extensions, format) })
}
