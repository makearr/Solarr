package book

import (
	"context"
	"path"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/works"
)

// ErrNotSearchable reports an item the book search does not handle: not the one wanted item of a book.
var ErrNotSearchable = works.ErrNotSearchable

// Search is the book specialisation of the shared candidate pipeline (works.Search): what a book wants, Readarr's
// queries by author and title, and whether a release is the book in the format's files.
type Search struct {
	works.Deps
	Format Format
	Reader library.SeriesReader
}

func (s Search) shared() works.Search[wanted] {
	return works.Search[wanted]{
		Name: s.Format.MediaType, Spec: bookSpec{s}, Deps: s.Deps,
	}
}

// SearchItem searches one book's item.
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// SearchPart implements works.Searcher; a book has no parts, so it is SearchItem.
func (s Search) SearchPart(ctx context.Context, itemID int64, _ int, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchItem(ctx, itemID, conns, at)
}

// JudgeFeed judges releases a feed listed against one wanted book, exactly as a search's would be.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error) {
	return s.shared().JudgeFeed(ctx, itemID, conn, raws, at)
}

// wanted is what a search needs to know of a book it looks for.
type wanted struct {
	title   string
	aliases []string
	authors []string
	// owned is, for an upgrade, the facts of the file the book has now: a release must strictly beat it.
	owned *score.Facts
}

// bookSpec is works.Spec for books and audiobooks.
type bookSpec struct{ search Search }

// Wanted implements works.Spec: a book wants its files until it has them, then an ebook a format that beats one below
// its cutoff. An audiobook is never upgraded: its chapters are one set, replaced only by hand.
func (b bookSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, _ int, now time.Time) (wanted, bool, error) {
	if err := works.WholeItem(item); err != nil {
		return wanted{}, false, err
	}
	s := b.search
	basics, err := s.Reader.Basics(ctx, item.WorkID)
	if err != nil {
		return wanted{}, false, err
	}
	facts, err := NamingFacts(ctx, s.Reader, s.Format, item.CategoryID, item.WorkID, now)
	if err != nil {
		return wanted{}, false, err
	}
	w := wanted{title: basics.Title, aliases: basics.Aliases}
	if facts.Author != "" && facts.Author != unknownAuthor {
		w.authors = []string{facts.Author}
	}
	if item.PresentCount == 0 {
		return w, true, nil
	}
	if s.Settings == nil || s.Format.ByFolder {
		return w, false, nil
	}
	files, err := fileFacts(ctx, s.Format, s.Reader, item.ID)
	if err != nil {
		return w, false, err
	}
	w.owned = works.OwnedBelowCutoff(ctx, s.Settings.Snapshot(), item.CategoryID, files, now)
	return w, w.owned != nil, nil
}

// Owned implements works.Spec.
func (bookSpec) Owned(w wanted) map[int]score.Facts {
	return works.OwnedOf(w.owned)
}

// Queries implements works.Spec: Newznab's book search by author and title, then the text releases use.
func (bookSpec) Queries(w wanted) [][]judge.Query {
	var tiers [][]judge.Query
	title := releasefacts.CleanQuery(w.title)
	if title == "" {
		return nil
	}
	var structured, withAuthor []judge.Query
	for _, author := range w.authors {
		clean := releasefacts.CleanQuery(author)
		structured = append(structured, judge.Query{Function: "book", Extra: map[string]string{"author": clean, "title": title}, Needs: []string{"author", "title"}})
		withAuthor = append(withAuthor, judge.Query{Function: "search", Text: clean + " " + title})
	}
	if len(structured) > 0 {
		tiers = append(tiers, structured, withAuthor)
	}
	bare := []judge.Query{{Function: "search", Text: title}}
	for _, alias := range w.aliases[:min(2, len(w.aliases))] {
		if clean := releasefacts.CleanQuery(alias); clean != "" && clean != title {
			bare = append(bare, judge.Query{Function: "search", Text: clean})
		}
	}
	return append(tiers, bare)
}

// Read implements works.Spec: a release is the book when it names one of its titles and one of its authors, as
// Readarr's search criteria do, and claims a file of the format.
func (b bookSpec) Read(w wanted, raw indexer.RawItem, conn judge.Conn, _ time.Time) (works.Release, bool) {
	info, _ := parsebook.Parse(raw.Title)
	if !w.isTheBook(raw.Title) || !b.search.Format.Claims(info) {
		return works.Release{}, false
	}
	return works.Release{Facts: ReleaseFacts(b.search.Format, info, raw, conn.Impl), ReleaseGroup: info.ReleaseGroup, Revised: info.Version > 1}, true
}

func (w wanted) isTheBook(name string) bool {
	for _, title := range append([]string{w.title}, w.aliases...) {
		if parsebook.Names(name, title, w.authors) {
			return true
		}
	}
	return false
}

// audioCodecOf is the codec an audiobook's container or extension carries.
var audioCodecOf = map[string]string{"m4b": "aac", "m4a": "aac", "aac": "aac", "mp4": "aac", "mp3": "mp3", "flac": "flac", "ogg": "vorbis", "opus": "opus", "wma": "wmav2"}

// ReleaseFacts stands what a release name claims in for the facts a profile judges: its format as the file's
// extension, and an audiobook's bitrate.
func ReleaseFacts(format Format, info parsebook.Info, raw indexer.RawItem, impl string) score.Facts {
	facts := score.Facts{
		Release: score.ReleaseFacts{Title: raw.Title, ReleaseGroup: info.ReleaseGroup, Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: judge.ProtocolFor(impl)},
		File:    score.FileFacts{ReleaseGroup: info.ReleaseGroup, ProperRepack: info.Version > 1},
	}
	for _, claimed := range info.Formats {
		if slices.Contains(format.Extensions, claimed) {
			facts.File.Extension = claimed
			break
		}
	}
	if format.ByFolder {
		facts.File.AudioCodec = audioCodecOf[facts.File.Extension]
		facts.File.Lossless = facts.File.Extension == "flac"
		if info.BitrateKbps > 0 {
			facts.File.AudioBitrate = int64(info.BitrateKbps) * 1000
		}
	}
	return facts
}

// fileFacts are a book's files as a profile judges them: an ebook by its extension, which is all there is to measure;
// an audiobook by what the probe measured, its extension where ffprobe says nothing.
func fileFacts(ctx context.Context, format Format, reader library.SeriesReader, itemID int64) ([]works.File, error) {
	files, err := reader.ItemFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]works.File, 0, len(files))
	for _, file := range files {
		held := works.File{MediaObjectID: file.File.MediaObjectID, AddedAt: file.AddedAt, Monitored: file.Monitored}
		extension := path.Ext(file.RelPath)
		held.Facts = score.Facts{File: score.FileFacts{Extension: extension, Container: file.File.Container}}
		if format.ByFolder {
			held.Facts.File.AudioCodec, held.Facts.File.AudioBitrate = file.File.AudioCodec, file.File.AudioBitrate
			held.Facts.File.Lossless = file.File.AudioCodec == "flac" || file.File.AudioCodec == "alac"
			held.Measured = file.File.AudioCodec != ""
		} else {
			held.Measured = extension != ""
		}
		out = append(out, held)
	}
	return out, nil
}
