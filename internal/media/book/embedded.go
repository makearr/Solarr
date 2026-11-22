package book

import (
	"context"

	"github.com/makearr/Solarr/internal/identify/bookmeta"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/iogate"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// withEmbedded reads what the book says of itself (a Calibre metadata.opf beside it, or an EPUB's own package file)
// and lays it over what its name gave: its ids win, its title and first author lead, and the name's title stays as a
// reading. Without metadata the name's reading is returned as it was.
func withEmbedded(ctx context.Context, fsys fsx.FS, absolutePath string, info parsebook.Info) parsebook.Info {
	if fsys == nil {
		return info
	}
	var meta bookmeta.Meta
	var ok bool
	// The share is read one file at a time, in the turn the probe takes on its disk.
	if err := iogate.OnDisk(ctx, "", []string{absolutePath}, func() error { meta, ok = bookmeta.Read(fsys, absolutePath); return nil }); err != nil || !ok {
		return info
	}
	return layered(info, meta)
}

// withTags is withEmbedded for an audiobook: what the tags of its first file say of the book is laid over its name's reading.
func withTags(info parsebook.Info, tags mediatool.Tags) parsebook.Info {
	meta, ok := bookmeta.FromTags(tags.First)
	if !ok {
		return info
	}
	return layered(info, meta)
}

func layered(info parsebook.Info, meta bookmeta.Meta) parsebook.Info {
	if meta.ISBN != "" {
		info.ISBN = meta.ISBN
	}
	if meta.ASIN != "" {
		info.ASIN = meta.ASIN
	}
	if meta.Title != "" && meta.Title != info.Title {
		info.TitleUnbacked = !namedByReadings(info, meta.Title)
		readings := []string{meta.Title}
		if info.Title != "" {
			readings = append(readings, info.Title)
		}
		info.Titles = append(readings, info.Titles...)
		info.Title = meta.Title
	}
	if len(meta.Authors) > 0 {
		info.Author = meta.Authors[0]
	}
	return info
}

// namedByReadings is true when a reading of the name, or none yet, says the title.
func namedByReadings(info parsebook.Info, title string) bool {
	if info.Title == "" {
		return true
	}
	wanted := parsebook.CleanTitle(title)
	for _, reading := range append([]string{info.Title}, info.Titles...) {
		if parsebook.CleanTitle(reading) == wanted {
			return true
		}
	}
	return false
}
