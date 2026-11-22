package book

import (
	"github.com/makearr/Solarr/internal/identify/bookmeta"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// withEmbedded reads what the book says of itself (a Calibre metadata.opf beside it, or an EPUB's own package file)
// and lays it over what its name gave: its ids win, its title and first author lead, and the name's title stays as a
// reading. Without metadata the name's reading is returned as it was.
func withEmbedded(fsys fsx.FS, absolutePath string, info parsebook.Info) parsebook.Info {
	if fsys == nil {
		return info
	}
	meta, ok := bookmeta.Read(fsys, absolutePath)
	if !ok {
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
