package book

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsebook"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports an item the book search does not handle: not the one wanted item of a book.
var ErrNotSearchable = errors.New("book: search: only a book's own item is searched")

// upgradeMinFileAge keeps a file that has just arrived from being replaced at once.
const upgradeMinFileAge = 24 * time.Hour

// Search is the book candidate pipeline over one wanted item at a time.
type Search struct {
	Format     Format
	Desired    library.Desired
	Reader     library.SeriesReader
	Physical   library.Physical
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the book's own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

// wanted is what a search needs to know of a book it looks for.
type wanted struct {
	title   string
	aliases []string
	authors []string
	// owned is, for an upgrade, the facts of the file the book has now: a release must strictly beat it.
	owned *score.Facts
}

func (s Search) wantedFor(ctx context.Context, itemID int64) (library.WantedItemDetail, wanted, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return library.WantedItemDetail{}, wanted{}, err
	}
	if item.SatisfactionKind != "SET" || item.MediaRef != "" || item.EditionID.Valid {
		return library.WantedItemDetail{}, wanted{}, ErrNotSearchable
	}
	basics, err := s.Reader.Basics(ctx, item.WorkID)
	if err != nil {
		return library.WantedItemDetail{}, wanted{}, err
	}
	facts, err := NamingFacts(ctx, s.Reader, s.Format, item.CategoryID, item.WorkID, s.Clock.Now())
	if err != nil {
		return library.WantedItemDetail{}, wanted{}, err
	}
	w := wanted{title: basics.Title, aliases: basics.Aliases}
	if facts.Author != "" && facts.Author != "Unknown Author" {
		w.authors = []string{facts.Author}
	}
	detail := library.WantedItemDetail{ItemID: item.ID, WorkID: item.WorkID, CategoryID: item.CategoryID, ProfileID: item.ProfileID, State: item.State, Present: item.PresentCount}
	if item.PresentCount > 0 {
		owned, err := s.belowCutoff(ctx, item.ID, item.CategoryID)
		if err != nil {
			return detail, w, err
		}
		w.owned = owned
	}
	return detail, w, nil
}

// belowCutoff is the facts of the file a book has that misses its library's cutoff, once it has settled; nil when it
// needs no upgrade. An audiobook is never upgraded: its chapters are one set, replaced only by hand.
func (s Search) belowCutoff(ctx context.Context, itemID, categoryID int64) (*score.Facts, error) {
	if s.Settings == nil || s.Format.ByFolder {
		return nil, nil
	}
	snapshot := s.Settings.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found || !category.QualityUpgrades {
		return nil, nil
	}
	files, err := fileFacts(ctx, s.Format, s.Reader, s.Physical, itemID)
	if err != nil {
		return nil, err
	}
	tooNew := s.Clock.Now().Add(-upgradeMinFileAge).Unix()
	for _, file := range files {
		if file.addedAt > tooNew {
			continue
		}
		if judged, ok := quality.Judge(snapshot, category, file.facts); ok && !judged.MeetsCutoff {
			return &file.facts, nil
		}
	}
	return nil, nil
}

// queries builds the tiers of one book's search: Newznab's book search by author and title, then the text releases use.
func (w wanted) queries() [][]judge.Query {
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

// SearchItem searches one book's item: tiers widen until one finds a release of the book, and every release that is
// one is judged and recorded (a REJECT or DEFER is a finding, not silence).
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (judge.Result, error) {
	detail, w, err := s.wantedFor(ctx, itemID)
	if err != nil {
		return judge.Result{}, err
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return judge.Result{}, fmt.Errorf("book: search item %d: %w", itemID, err)
	}
	if detail.Present > 0 && w.owned == nil {
		return judge.Result{}, nil
	}
	yardstick, err := s.yardstick(ctx, detail, w)
	if err != nil {
		return judge.Result{}, fmt.Errorf("book: search item %d: %w", itemID, err)
	}
	result, err := s.pipeline().Search(ctx, itemID, conns, w.queries(), yardstick.evaluate, at)
	if err != nil {
		return result, fmt.Errorf("book: search item %d: %w", itemID, err)
	}
	return result, nil
}

// JudgeFeed judges releases a feed listed against one wanted book, exactly as a search's would be.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error) {
	detail, w, err := s.wantedFor(ctx, itemID)
	if err != nil || (detail.Present > 0 && w.owned == nil) {
		return judge.Result{}, err
	}
	measure, err := s.yardstick(ctx, detail, w)
	if err != nil {
		return judge.Result{}, err
	}
	return s.pipeline().Feed(ctx, itemID, conn, raws, measure.evaluate, at)
}

func (s Search) pipeline() judge.Pipeline {
	return judge.Pipeline{Candidates: s.Candidates, Desired: s.Desired, Sightings: s.Sightings, Blocklist: s.Blocklist, Deadline: s.IndexerDeadline}
}

// yardstick judges a release against the book and the library's rules.
type yardstick struct {
	format         Format
	wanted         wanted
	rules          []judge.RankedProfile
	languagePolicy *score.LanguagePolicy
	preferPropers  bool
	delay          judge.ProtocolDelay
	incumbent      *model.Decision
	filters        []judge.ReleaseFilter
}

func (s Search) yardstick(ctx context.Context, detail library.WantedItemDetail, w wanted) (yardstick, error) {
	snapshot := s.Settings.Snapshot()
	rules, err := judge.LibraryProfiles(ctx, s.Desired, snapshot, detail.ItemID, detail.CategoryID, detail.ProfileID)
	if err != nil {
		return yardstick{}, err
	}
	built := yardstick{format: s.Format, wanted: w, rules: rules, languagePolicy: judge.LanguagePolicyFor(snapshot, detail.CategoryID), preferPropers: true}
	if w.owned != nil {
		owned := judge.EvaluateInOrder(rules, *w.owned, built.languagePolicy)
		built.incumbent = &owned
	}
	if category, found := snapshot.Category(detail.CategoryID); found {
		built.preferPropers = category.FileManagement.PropersRepacks != settings.PropersDoNotPrefer
		built.delay = judge.ProtocolDelay{Preferred: category.PreferredProtocol, Window: time.Duration(category.ProtocolDelayMinutes) * time.Minute, Now: s.Clock.Now()}
	}
	if s.ReleaseFilters != nil {
		stored, err := s.ReleaseFilters.List(ctx, detail.WorkID)
		if err != nil {
			return yardstick{}, err
		}
		for _, filter := range stored {
			built.filters = append(built.filters, judge.ReleaseFilter{Prefix: filter.Prefix, Kind: filter.Kind, Value: filter.Value, IsRegex: filter.IsRegex, Score: int(filter.Score)})
		}
	}
	return built, nil
}

func (y yardstick) evaluate(raws []indexer.RawItem, conn judge.Conn) []judge.Find {
	var finds []judge.Find
	for _, raw := range raws {
		info, _ := parsebook.Parse(raw.Title)
		if !y.isTheBook(raw.Title) || !y.format.Claims(info) {
			continue
		}
		facts := ReleaseFacts(y.format, info, raw, conn.Impl)
		decision := judge.WithRevision(judge.EvaluateInOrder(y.rules, facts, y.languagePolicy), info.Version > 1, y.preferPropers)
		decision = judge.ApplyFilters(decision, y.filters, raw.Title, info.ReleaseGroup, "")
		y.requireUpgrade(&decision, facts)
		finds = append(finds, judge.Find{Conn: conn, Raw: raw, ParseConfidence: 1, MatchConfidence: 1, Decision: decision})
	}
	y.delay.Apply(finds)
	return finds
}

// isTheBook says a release names this book: one of its titles and one of its authors, as Readarr's search criteria do.
func (y yardstick) isTheBook(name string) bool {
	for _, title := range append([]string{y.wanted.title}, y.wanted.aliases...) {
		if parsebook.Names(name, title, y.wanted.authors) {
			return true
		}
	}
	return false
}

// requireUpgrade rejects an accepted release that does not beat the book's file by quality (Invariant 72: a tie keeps
// what is owned).
func (y yardstick) requireUpgrade(decision *model.Decision, facts score.Facts) {
	if decision.Outcome != model.OutcomeAccept || y.incumbent == nil {
		return
	}
	facts.File.ReleaseGroup, facts.File.ProperRepack = "", false
	likeForLike := judge.EvaluateInOrder(y.rules, facts, y.languagePolicy)
	if winner, _ := model.Compare(likeForLike.Quality, y.incumbent.Quality); winner >= 0 {
		decision.Outcome = model.OutcomeReject
		decision.Rejections = append(decision.Rejections, model.Rejection{
			Rule: "upgrade", Field: "quality", Wanted: "better than the file you have", Got: "not better",
		})
	}
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

// heldFile is one file of a book with what is known of it.
type heldFile struct {
	mediaObjectID int64
	addedAt       int64
	measured      bool
	facts         score.Facts
}

// fileFacts are a book's files as a profile judges them: an ebook by its extension, which is all there is to measure;
// an audiobook by what the probe measured, its extension where ffprobe says nothing.
func fileFacts(ctx context.Context, format Format, reader library.SeriesReader, physical library.Physical, itemID int64) ([]heldFile, error) {
	files, err := reader.ItemFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]heldFile, 0, len(files))
	for _, file := range files {
		held := heldFile{mediaObjectID: file.File.MediaObjectID, addedAt: file.AddedAt}
		paths, err := physical.PathsForObject(ctx, file.File.MediaObjectID)
		if err != nil {
			return nil, err
		}
		extension := ""
		if len(paths) > 0 {
			extension = path.Ext(paths[0].RelPath)
		}
		held.facts = score.Facts{File: score.FileFacts{Extension: extension, Container: file.File.Container}}
		if format.ByFolder {
			held.facts.File.AudioCodec, held.facts.File.AudioBitrate = file.File.AudioCodec, file.File.AudioBitrate
			held.facts.File.Lossless = file.File.AudioCodec == "flac" || file.File.AudioCodec == "alac"
			held.measured = file.File.AudioCodec != ""
		} else {
			held.measured = extension != ""
		}
		out = append(out, held)
	}
	return out, nil
}
