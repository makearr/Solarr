package works

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrNotSearchable reports an item a media type does not search: not the one wanted item of a work it knows.
var ErrNotSearchable = errors.New("works: search: the item is not one the media type searches")

// UpgradeMinFileAge keeps a file that has just arrived from being replaced at once.
const UpgradeMinFileAge = 24 * time.Hour

// Spec is a media type's part of a search: what an item wants (W is the type's own account of it), the queries that
// look for it, and what a release is to it. The judging, the library's rules and the upgrade rule are the same for all.
type Spec[W any] interface {
	// Wanted reads what the item wants, narrowed to one part (zero is whatever it lacks); needed is false when it
	// wants nothing now. ErrNotSearchable for an item the type does not search.
	Wanted(ctx context.Context, item librarydb.DesiredItem, part int, now time.Time) (wanted W, needed bool, err error)
	// Queries are the tiers of the search: each is asked only when the one before found nothing that is wanted.
	Queries(wanted W) [][]judge.Query
	// Owned are the facts of the files an upgrade would replace, by part (0 for a work without parts); nil when the
	// search fills a gap.
	Owned(wanted W) map[int]score.Facts
	// Read is what one release is to the item; false when it is none of what is wanted.
	Read(wanted W, raw indexer.RawItem, conn judge.Conn, now time.Time) (Release, bool)
}

// Release is one release as a media type reads it.
type Release struct {
	// Facts are what its name claims, standing in for the file until it arrives (ADR-39).
	Facts        score.Facts
	ReleaseGroup string
	// Revised is a PROPER, REPACK or v2.
	Revised bool
	// Parts are the wanted parts it holds; nil is the whole work, part 0.
	Parts []int
	// Held is how many parts it holds in all, wanted or not: a season pack holds the season.
	Held int
	// Share is how much of what is wanted it holds, 0 to 1; zero is all of it.
	Share float64
}

// Search is the candidate pipeline over one wanted item at a time, a media type's Spec saying what is wanted.
type Search[W any] struct {
	// Name prefixes errors ("movie").
	Name       string
	Spec       Spec[W]
	Desired    library.Desired
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	Blocklist  *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// ReleaseFilters, when set, hold every release against the work's own required, denied and preferred words and groups.
	ReleaseFilters *library.ReleaseFilters
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

// SearchItem searches what one item is missing.
func (s Search[W]) SearchItem(ctx context.Context, itemID int64, conns []judge.Conn, at time.Time) (judge.Result, error) {
	return s.SearchPart(ctx, itemID, 0, conns, at)
}

// SearchPart searches one item: tiers widen until one finds a release of something wanted, and every release that is
// one is judged and recorded (a REJECT or DEFER is a finding, not silence). part narrows it to one part; zero searches
// what the item is missing.
func (s Search[W]) SearchPart(ctx context.Context, itemID int64, part int, conns []judge.Conn, at time.Time) (judge.Result, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return judge.Result{}, fmt.Errorf("%s: search item %d: %w", s.Name, itemID, err)
	}
	wanted, needed, err := s.Spec.Wanted(ctx, item, part, s.Clock.Now())
	if err != nil {
		return judge.Result{}, err
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return judge.Result{}, fmt.Errorf("%s: search item %d: %w", s.Name, itemID, err)
	}
	if !needed {
		return judge.Result{}, nil
	}
	measure, err := s.yardstick(ctx, item, wanted)
	if err != nil {
		return judge.Result{}, fmt.Errorf("%s: search item %d: %w", s.Name, itemID, err)
	}
	result, err := s.pipeline().Search(ctx, itemID, conns, s.Spec.Queries(wanted), measure.evaluate, at)
	if err != nil {
		return result, fmt.Errorf("%s: search item %d: %w", s.Name, itemID, err)
	}
	return result, nil
}

// JudgeFeed judges releases a feed listed against one wanted item, exactly as a search's would be; nothing is searched
// and the item's search record is left alone.
func (s Search[W]) JudgeFeed(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (judge.Result, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return judge.Result{}, err
	}
	wanted, needed, err := s.Spec.Wanted(ctx, item, 0, s.Clock.Now())
	if err != nil || !needed {
		return judge.Result{}, err
	}
	measure, err := s.yardstick(ctx, item, wanted)
	if err != nil {
		return judge.Result{}, err
	}
	return s.pipeline().Feed(ctx, itemID, conn, raws, measure.evaluate, at)
}

func (s Search[W]) pipeline() judge.Pipeline {
	return judge.Pipeline{Candidates: s.Candidates, Desired: s.Desired, Sightings: s.Sightings, Blocklist: s.Blocklist, Deadline: s.IndexerDeadline}
}

// yardstick judges a release against what the item wants and the library's rules.
type yardstick[W any] struct {
	spec           Spec[W]
	wanted         W
	rules          []judge.RankedProfile
	languagePolicy *score.LanguagePolicy
	preferPropers  bool
	delay          judge.ProtocolDelay
	now            time.Time
	// incumbents are the decisions the files an upgrade replaces would get, by part; empty when it fills a gap.
	incumbents map[int]model.Decision
	filters    []judge.ReleaseFilter
	mediaRef   string
}

func (s Search[W]) yardstick(ctx context.Context, item librarydb.DesiredItem, wanted W) (yardstick[W], error) {
	snapshot := s.Settings.Snapshot()
	rules, err := judge.LibraryProfiles(ctx, s.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if err != nil {
		return yardstick[W]{}, err
	}
	built := yardstick[W]{
		spec: s.Spec, wanted: wanted, rules: rules, languagePolicy: judge.LanguagePolicyFor(snapshot, item.CategoryID),
		preferPropers: true, now: s.Clock.Now(), incumbents: map[int]model.Decision{}, mediaRef: item.MediaRef,
	}
	if s.ReleaseFilters != nil {
		stored, err := s.ReleaseFilters.List(ctx, item.WorkID)
		if err != nil {
			return yardstick[W]{}, err
		}
		for _, filter := range stored {
			built.filters = append(built.filters, judge.ReleaseFilter{
				Prefix: filter.Prefix, Kind: filter.Kind, Value: filter.Value, IsRegex: filter.IsRegex, Score: int(filter.Score),
			})
		}
	}
	for part, facts := range s.Spec.Owned(wanted) {
		built.incumbents[part] = judge.EvaluateInOrder(rules, facts, built.languagePolicy)
	}
	if category, found := snapshot.Category(item.CategoryID); found {
		built.preferPropers = category.FileManagement.PropersRepacks != settings.PropersDoNotPrefer
		built.delay = judge.ProtocolDelay{Preferred: category.PreferredProtocol, Window: time.Duration(category.ProtocolDelayMinutes) * time.Minute, Now: built.now}
	}
	return built, nil
}

func (y yardstick[W]) evaluate(raws []indexer.RawItem, conn judge.Conn) []judge.Find {
	var finds []judge.Find
	for _, raw := range raws {
		release, ok := y.spec.Read(y.wanted, raw, conn, y.now)
		if !ok {
			continue
		}
		decision := judge.WithRevision(judge.EvaluateInOrder(y.rules, release.Facts, y.languagePolicy), release.Revised, y.preferPropers)
		decision = judge.ApplyFilters(decision, y.filters, raw.Title, release.ReleaseGroup, y.mediaRef)
		y.requireUpgrade(&decision, release)
		find := judge.Find{Conn: conn, Raw: raw, ParseConfidence: 1, MatchConfidence: 1, Decision: decision}
		if release.Share > 0 {
			find.Distance, find.MatchConfidence = 1-release.Share, max(0.5, release.Share)
		}
		finds = append(finds, find)
	}
	y.delay.Apply(finds)
	return finds
}

// requireUpgrade rejects an accepted release that does not beat the file of every part it would replace by quality
// (Invariant 72: a tie keeps what is owned). Only what a file can show is compared: a release name states a source, a
// group and a repack that the measured file does not, so with them it would always out-rank the file it replaces.
func (y yardstick[W]) requireUpgrade(decision *model.Decision, release Release) {
	if decision.Outcome != model.OutcomeAccept || len(y.incumbents) == 0 {
		return
	}
	parts := release.Parts
	if parts == nil {
		parts = []int{0}
	}
	// A pack mostly of parts that are fine as they are would place a second copy of each.
	if release.Held > 1 && len(parts)*2 < release.Held {
		decision.Outcome = model.OutcomeReject
		decision.Rejections = append(decision.Rejections, model.Rejection{
			Rule: "upgrade", Field: "pack", Wanted: "a pack mostly of episodes below the cutoff", Got: "most of it is already good enough",
		})
		return
	}
	facts := release.Facts
	facts.File.Source, facts.File.ReleaseGroup, facts.File.ProperRepack = "", "", false
	likeForLike := judge.EvaluateInOrder(y.rules, facts, y.languagePolicy)
	wanted := "better than the files you have"
	if release.Parts == nil {
		wanted = "better than the file you have"
	}
	for _, part := range parts {
		owned, replacing := y.incumbents[part]
		if !replacing {
			continue
		}
		if winner, _ := model.Compare(likeForLike.Quality, owned.Quality); winner >= 0 {
			decision.Outcome = model.OutcomeReject
			decision.Rejections = append(decision.Rejections, model.Rejection{Rule: "upgrade", Field: "quality", Wanted: wanted, Got: "not better"})
			return
		}
	}
}

// WholeItem says an item is the one wanted item of a work without parts; ErrNotSearchable otherwise.
func WholeItem(item librarydb.DesiredItem) error {
	if item.SatisfactionKind != "SET" || item.MediaRef != "" || item.EditionID.Valid {
		return ErrNotSearchable
	}
	return nil
}
