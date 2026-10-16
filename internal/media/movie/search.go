package movie

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/releasefacts"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parsemovie"
	"github.com/makearr/Solarr/internal/identify/parsetv"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports an item the movie search does not handle: not the one wanted item of a movie.
var ErrNotSearchable = errors.New("movie: search: only a movie's own item is searched")

// Item states the cutoff check and the search move a movie between.
const (
	ItemStateUpgradable = "UPGRADABLE"
	ItemStateComplete   = "COMPLETE"
)

// ItemSearchResult is one movie's search; the shared result under the movie name.
type ItemSearchResult = judge.Result

// upgradeMinFileAge keeps a file that has just arrived from being replaced at once.
const upgradeMinFileAge = 24 * time.Hour

// Search is the movie candidate pipeline over one wanted item at a time.
type Search struct {
	Desired    library.Desired
	Reader     library.SeriesReader
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the movie's own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

// wanted is what a search needs to know of a movie it looks for.
type wanted struct {
	title     string
	aliases   []string
	year      int
	tmdb      string
	imdb      string
	runtimeMs int64
	// owned is, for an upgrade, the file the movie has now: a release must strictly beat it.
	owned *library.EpisodeFile
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
	ids, err := s.Reader.ProviderIDs(ctx, item.WorkID)
	if err != nil {
		return library.WantedItemDetail{}, wanted{}, err
	}
	w := wanted{title: basics.Title, aliases: basics.Aliases, year: int(basics.Year), tmdb: ids[ProviderTMDB], imdb: ids[ProviderIMDb], runtimeMs: basics.RuntimeMs}
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

// belowCutoff is the file a movie has that misses its library's cutoff, once it has settled; nil when it needs no upgrade.
func (s Search) belowCutoff(ctx context.Context, itemID, categoryID int64) (*library.EpisodeFile, error) {
	if s.Settings == nil {
		return nil, nil
	}
	snapshot := s.Settings.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found || !category.QualityUpgrades {
		return nil, nil
	}
	files, err := s.Reader.ItemFiles(ctx, itemID)
	if err != nil {
		return nil, err
	}
	tooNew := s.Clock.Now().Add(-upgradeMinFileAge).Unix()
	for _, file := range files {
		if !file.Measured || file.AddedAt > tooNew {
			continue
		}
		if judged, ok := quality.Judge(snapshot, category, releasefacts.FromFile(file.File)); ok && !judged.MeetsCutoff {
			return &file.File, nil
		}
	}
	return nil, nil
}

// titles are every name a release may call the movie by, cleaned for comparison.
func (w wanted) titles() map[string]bool {
	names := map[string]bool{}
	for _, title := range append([]string{w.title}, w.aliases...) {
		if clean := parsetv.CleanTitle(title); clean != "" {
			names[clean] = true
		}
	}
	return names
}

// queries builds the tiers of one movie's search: the id queries first, then text in the spellings releases use.
func (w wanted) queries() [][]judge.Query {
	var tiers [][]judge.Query
	var byID []judge.Query
	if w.imdb != "" {
		byID = append(byID, judge.Query{Function: "movie", Extra: map[string]string{"imdbid": strings.TrimPrefix(w.imdb, "tt")}, Needs: []string{"imdbid"}})
	}
	if w.tmdb != "" {
		byID = append(byID, judge.Query{Function: "movie", Extra: map[string]string{"tmdbid": w.tmdb}, Needs: []string{"tmdbid"}})
	}
	if len(byID) > 0 {
		tiers = append(tiers, byID)
	}
	names := append([]string{w.title}, w.aliases...)
	if len(names) > 3 {
		names = names[:3]
	}
	var withYear, bare []judge.Query
	for _, name := range names {
		clean := releasefacts.CleanQuery(name)
		if clean == "" {
			continue
		}
		bare = append(bare, judge.Query{Function: "search", Text: clean})
		if w.year > 0 {
			withYear = append(withYear, judge.Query{Function: "search", Text: fmt.Sprintf("%s %d", clean, w.year)})
		}
	}
	if len(withYear) > 0 {
		tiers = append(tiers, withYear)
	}
	return append(tiers, bare)
}

// SearchItem searches one movie's item: tiers widen until one finds a release of the movie, and every release that is
// one is judged and recorded (a REJECT or DEFER is a finding, not silence).
func (s Search) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (ItemSearchResult, error) {
	detail, w, err := s.wantedFor(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, err
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return ItemSearchResult{}, fmt.Errorf("movie: search item %d: %w", itemID, err)
	}
	if detail.Present > 0 && w.owned == nil {
		return ItemSearchResult{}, nil
	}
	yardstick, err := s.yardstick(ctx, detail, w)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("movie: search item %d: %w", itemID, err)
	}
	result, err := s.pipeline().Search(ctx, itemID, conns, w.queries(), yardstick.evaluate, at)
	if err != nil {
		return result, fmt.Errorf("movie: search item %d: %w", itemID, err)
	}
	return result, nil
}

// JudgeFeed judges releases a feed listed against one wanted movie, exactly as a search's would be; nothing is
// searched and the item's search record is left alone.
func (s Search) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (ItemSearchResult, error) {
	detail, w, err := s.wantedFor(ctx, itemID)
	if err != nil || (detail.Present > 0 && w.owned == nil) {
		return ItemSearchResult{}, err
	}
	measure, err := s.yardstick(ctx, detail, w)
	if err != nil {
		return ItemSearchResult{}, err
	}
	return s.pipeline().Feed(ctx, itemID, conn, raws, measure.evaluate, at)
}

func (s Search) pipeline() judge.Pipeline {
	return judge.Pipeline{Candidates: s.Candidates, Desired: s.Desired, Sightings: s.Sightings, Blocklist: s.Blocklist, Deadline: s.IndexerDeadline}
}

// yardstick judges a release against the movie and the library's rules.
type yardstick struct {
	wanted         wanted
	titles         map[string]bool
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
	built := yardstick{wanted: w, titles: w.titles(), rules: rules, languagePolicy: judge.LanguagePolicyFor(snapshot, detail.CategoryID), preferPropers: true}
	if w.owned != nil {
		owned := judge.EvaluateInOrder(rules, releasefacts.FromFile(*w.owned), built.languagePolicy)
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
		info, ok := parsemovie.Parse(raw.Title)
		if !ok || !y.isTheMovie(info) {
			continue
		}
		facts := releasefacts.FromRelease(info.Claims, info.ReleaseGroup, raw, conn.Impl)
		releasefacts.EstimateBitrate(&facts, raw.SizeBytes, 1, y.wanted.runtimeMs)
		decision := judge.WithRevision(judge.EvaluateInOrder(y.rules, facts, y.languagePolicy), info.Claims.Version > 1, y.preferPropers)
		decision = judge.ApplyFilters(decision, y.filters, raw.Title, info.ReleaseGroup, "")
		y.requireUpgrade(&decision, facts)
		finds = append(finds, judge.Find{Conn: conn, Raw: raw, ParseConfidence: 1, MatchConfidence: 1, Decision: decision})
	}
	y.delay.Apply(finds)
	return finds
}

// isTheMovie says a release names this movie: one of its titles, in its year give or take one. A release with no year is
// not taken: a title alone cannot tell a film from its remake.
func (y yardstick) isTheMovie(info parsemovie.Info) bool {
	named := false
	for _, title := range append([]string{info.Title}, info.AllTitles...) {
		if y.titles[parsetv.CleanTitle(title)] {
			named = true
		}
	}
	return named && yearsAgree(y.wanted.year, info.Year)
}

// requireUpgrade rejects an accepted release that does not beat the file the movie has now by quality (Invariant 72: a
// tie keeps what is owned). Only what a file can show is compared: a name states a source, a group and a repack that the
// measured file does not, so with them it would always out-rank the file it replaces.
func (y yardstick) requireUpgrade(decision *model.Decision, facts score.Facts) {
	if decision.Outcome != model.OutcomeAccept || y.incumbent == nil {
		return
	}
	facts.File.Source, facts.File.ReleaseGroup, facts.File.ProperRepack = "", "", false
	likeForLike := judge.EvaluateInOrder(y.rules, facts, y.languagePolicy)
	if winner, _ := model.Compare(likeForLike.Quality, y.incumbent.Quality); winner >= 0 {
		decision.Outcome = model.OutcomeReject
		decision.Rejections = append(decision.Rejections, model.Rejection{
			Rule: "upgrade", Field: "quality", Wanted: "better than the file you have", Got: "not better",
		})
	}
}
