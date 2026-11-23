package comic

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsecomic"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

// ErrNotSearchable reports an item the comic search does not handle.
var ErrNotSearchable = works.ErrNotSearchable

// Search is the comic specialisation of the shared candidate pipeline: a wanted item is one kind of part of a series
// (its issues, annuals, volumes or chapters), and its parts are searched by number.
type Search struct {
	works.Deps
	Reader library.SeriesReader
	// KindOf answers a library's kind; nil takes Comics.
	KindOf func(categoryID int64) string
	// OptionOf answers one of the library's own settings; nil takes every fallback.
	OptionOf func(categoryID int64, key, fallback string) string
}

func (s Search) shared() works.Search[wanted] {
	return works.Search[wanted]{Name: MediaType, Spec: comicSpec{s}, Deps: s.Deps}
}

// SearchItem searches what one item is missing.
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchPart(ctx, itemID, 0, conns, at)
}

// SearchPart implements works.Searcher: a part of an item is one whole-numbered issue, volume or chapter.
func (s Search) SearchPart(ctx context.Context, itemID int64, part int, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.shared().SearchPart(ctx, itemID, part, conns, at)
}

// JudgeFeed judges releases a feed listed against one wanted item, exactly as a search's would be.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error) {
	return s.shared().JudgeFeed(ctx, itemID, conn, raws, at)
}

// Release sizes no real archive falls outside: a file of a few kilobytes is a stub, a single issue over 2 GiB a bundle.
const (
	releaseFloorBytes   = 1 << 20
	singleCeilingBytes  = 2 << 30
	maxSearchedPerQuery = 4
)

// wanted is what a search needs to know of the parts an item lacks.
type wanted struct {
	kind    string
	manga   bool
	title   string
	aliases []string
	year    int64
	// missing are the wanted parts by whole number; those with a decimal or a suffix are asked for by the pull of a
	// pack, not alone.
	missing map[int]string
	names   map[string]bool
}

type comicSpec struct{ search Search }

// Wanted implements works.Spec: the monitored parts of the item's kind that have come out and have no file.
func (c comicSpec) Wanted(ctx context.Context, item librarydb.DesiredItem, only int, _ time.Time) (wanted, bool, error) {
	if item.SatisfactionKind != "SET" || item.MediaRef == "" {
		return wanted{}, false, ErrNotSearchable
	}
	s := c.search
	now := s.Clock.Now()
	detail, err := s.Reader.GetOf(ctx, MediaType, item.CategoryID, item.WorkID, now)
	if err != nil {
		return wanted{}, false, fmt.Errorf("series %d: %w", item.WorkID, err)
	}
	kind := KindComics
	if s.KindOf != nil {
		kind = s.KindOf(item.CategoryID)
	}
	w := wanted{kind: item.MediaRef, manga: isMangaKind(kind), title: detail.Summary.Title, aliases: detail.Aliases, year: detail.Summary.Year, missing: map[int]string{}, names: map[string]bool{}}
	for _, title := range append([]string{w.title}, w.aliases...) {
		if key := titleKey(title); key != "" {
			w.names[key] = true
		}
	}
	editionsSatisfy := s.OptionOf != nil && s.OptionOf(item.CategoryID, string(media.FileEditionSatisfies), EditionOff) == EditionOn
	for _, part := range detail.Episodes {
		if standsInEdition(editionsSatisfy, part) {
			continue
		}
		partKind, number, _ := strings.Cut(part.MediaRef, ":")
		whole, err := strconv.Atoi(number)
		if partKind != item.MediaRef || err != nil || whole < 0 || strconv.Itoa(whole) != number {
			continue
		}
		aired := library.PartOut(part.AvailableAt, part.AvailableGrace, part.Date, "", now) || (part.AvailableAt == 0 && part.Date == "")
		switch {
		case only > 0:
			if whole == only {
				w.missing[whole] = number
			}
		case part.Monitored && !part.Present && aired:
			w.missing[whole] = number
		}
	}
	return w, len(w.missing) > 0, nil
}

// standsInEdition is a part a held collected edition stands for, with the library's setting on: it is not searched for.
func standsInEdition(setting bool, part library.SeriesEpisode) bool {
	return setting && part.InEdition != nil
}

// Owned implements works.Spec: a search fills gaps; upgrades are the cutoff check's.
func (comicSpec) Owned(wanted) map[int]score.Facts { return nil }

// Queries implements works.Spec: the title with the number as releases print it, never a number alone; the bare title
// last, for packs.
func (comicSpec) Queries(w wanted) [][]judge.Query {
	title := releasefacts.CleanQuery(w.title)
	if title == "" {
		return nil
	}
	numbers := make([]int, 0, len(w.missing))
	for number := range w.missing {
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	numbers = numbers[:min(len(numbers), maxSearchedPerQuery)]
	var plain, marked, dated []judge.Query
	for _, number := range numbers {
		switch w.kind {
		case PartVolume:
			plain = append(plain, text(fmt.Sprintf("%s v%02d", title, number)))
			marked = append(marked, text(fmt.Sprintf("%s Vol %d", title, number)), text(fmt.Sprintf("%s Volume %02d", title, number)))
		case PartChapter:
			plain = append(plain, text(fmt.Sprintf("%s c%03d", title, number)))
			marked = append(marked, text(fmt.Sprintf("%s Chapter %d", title, number)))
		case PartAnnual:
			plain = append(plain, text(fmt.Sprintf("%s Annual %03d", title, number)))
			marked = append(marked, text(fmt.Sprintf("%s Annual #%d", title, number)))
		default:
			plain = append(plain, text(fmt.Sprintf("%s %03d", title, number)))
			marked = append(marked, text(fmt.Sprintf("%s #%d", title, number)))
			if w.year > 0 {
				dated = append(dated, text(fmt.Sprintf("%s %03d %d", title, number, w.year)))
			}
		}
	}
	tiers := [][]judge.Query{plain, marked}
	if len(dated) > 0 {
		tiers = append(tiers, dated)
	}
	bare := title
	if w.year > 0 && !w.manga {
		bare = fmt.Sprintf("%s %d", title, w.year)
	}
	return append(tiers, []judge.Query{text(bare)})
}

func text(term string) judge.Query { return judge.Query{Function: "search", Text: term} }

// Read implements works.Spec: a release is wanted when it names the series (or an alias, in the year when it states
// one) and holds a wanted part, alone or in a range.
func (comicSpec) Read(w wanted, raw indexer.RawItem, conn judge.Conn, _ time.Time) (works.Release, bool) {
	claim := parsecomic.Parse(raw.Title)
	if !w.names[titleKey(claim.Series)] {
		return works.Release{}, false
	}
	if claim.Year > 0 && w.year > 0 && (int64(claim.Year)-w.year > 1 || w.year-int64(claim.Year) > 1) {
		return works.Release{}, false
	}
	held := heldNumbers(w, claim)
	var parts []int
	for _, number := range held {
		if _, wantedPart := w.missing[number]; wantedPart {
			parts = append(parts, number)
		}
	}
	if len(parts) == 0 {
		return works.Release{}, false
	}
	return works.Release{
		Plausible: func(decision *model.Decision) { requirePlausibleArchiveSize(decision, raw.SizeBytes, len(held)) },
		Facts:     ReleaseFacts(claim, raw, conn.Impl), ReleaseGroup: claim.Group, Revised: claim.Edition == "repack" || claim.Edition == "proper" || claim.Edition == "fixed",
		Parts: parts, Held: len(held), Share: float64(len(parts)) / float64(len(w.missing)),
	}, true
}

// requirePlausibleArchiveSize rejects an accepted release whose size cannot be its named parts: a stub per part, or a
// single issue the size of a bundle.
func requirePlausibleArchiveSize(decision *model.Decision, sizeBytes int64, parts int) {
	if sizeBytes <= 0 || parts <= 0 || decision.Outcome != model.OutcomeAccept {
		return
	}
	perPart := sizeBytes / int64(parts)
	switch {
	case perPart < releaseFloorBytes:
		decision.Outcome = model.OutcomeReject
		decision.Rejections = append(decision.Rejections, model.Rejection{Rule: works.RuleImplausibleSize, Field: "size",
			Wanted: fmt.Sprintf("at least %d bytes a part", releaseFloorBytes), Got: fmt.Sprintf("about %d bytes a part", perPart)})
	case parts == 1 && sizeBytes > singleCeilingBytes:
		decision.Outcome = model.OutcomeReject
		decision.Rejections = append(decision.Rejections, model.Rejection{Rule: works.RuleImplausibleSize, Field: "size",
			Wanted: fmt.Sprintf("at most %d bytes for one part", singleCeilingBytes), Got: fmt.Sprintf("%d bytes", sizeBytes)})
	}
}

// heldNumbers are the whole numbers of the wanted kind a release names.
func heldNumbers(w wanted, claim parsecomic.Claim) []int {
	var numbers []int
	switch {
	case w.kind == PartVolume && claim.Volume > 0 && claim.Chapter == "":
		for n := claim.Volume; n <= max(claim.Volume, claim.VolumeEnd) && n-claim.Volume < maxRange; n++ {
			numbers = append(numbers, n)
		}
	case w.kind == PartChapter && claim.Chapter != "":
		for _, number := range numbersBetween(claim.Chapter, claim.ChapterEnd) {
			if n, err := strconv.Atoi(number); err == nil {
				numbers = append(numbers, n)
			}
		}
	case (w.kind == PartIssue && claim.Format != "annual" || w.kind == PartAnnual && claim.Format == "annual") && claim.IssueNumber != "":
		for _, number := range numbersBetween(claim.IssueNumber, claim.IssueEnd) {
			if n, err := strconv.Atoi(number); err == nil {
				numbers = append(numbers, n)
			}
		}
	}
	return numbers
}

// ReleaseFacts stands what a release name claims in for the facts a profile judges: its archive format, source and group.
func ReleaseFacts(claim parsecomic.Claim, raw indexer.RawItem, impl string) score.Facts {
	return score.Facts{
		Release: score.ReleaseFacts{Title: raw.Title, ReleaseGroup: claim.Group, Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: judge.ProtocolFor(impl)},
		File: score.FileFacts{
			Extension: claim.Extension, Container: claim.Extension, Source: claim.Source, ReleaseGroup: claim.Group,
			ProperRepack: claim.Edition == "repack" || claim.Edition == "proper" || claim.Edition == "fixed",
		},
	}
}
