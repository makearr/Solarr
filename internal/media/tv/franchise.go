package tv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/anilist"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// ProviderWikidata names the franchise a series is part of by its Wikidata item.
const ProviderWikidata = "wikidata"

// FranchiseSource is Wikidata as the franchise pass asks it: the franchise of a series and the series of a franchise.
type FranchiseSource interface {
	SeriesFranchise(ctx context.Context, tvdb, tmdb int64) (wikidata.Franchise, bool, error)
	FranchiseSeriesOf(ctx context.Context, qid string) ([]wikidata.FranchiseSeries, error)
}

// FranchisePassJobType is the registered type of the pass that finds the franchises series belong to.
const FranchisePassJobType = jobs.Type("tv.franchises")

// FranchisePassDefinition declares the pass: background metadata work, retried on transient failure.
var FranchisePassDefinition = jobs.Definition{
	Name: "Finding the franchises series belong to",
	Type: FranchisePassJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// AnimeRelations is AniList as the franchise pass asks it: what an anime is a sequel, prequel or spin-off of.
type AnimeRelations interface {
	Related(ctx context.Context, id int64) ([]anilist.Relation, error)
}

// FranchisePass finds which series belong together and keeps each family in one collection. Wikidata names franchises,
// AniList relates anime, names and crossover episodes (and a creator and network in common) relate the rest; every
// source's findings are then gathered, so a family is one collection whatever found it.
type FranchisePass struct {
	Source     FranchiseSource
	Anime      AnimeRelations
	Franchises library.Franchises
	Crossovers library.Crossovers
	Series     library.Series
	Logger     *slog.Logger
}

// relatedTypes are the AniList relations that put two entries in one franchise.
var relatedTypes = map[string]bool{"SEQUEL": true, "PREQUEL": true, "SPIN_OFF": true, "SIDE_STORY": true, "PARENT": true, "ALTERNATIVE": true}

// animeCrawlBudget bounds the AniList calls one entry's relations may take.
const animeCrawlBudget = 12

// animePerRun bounds the anime entries one run reads relations of.
const animePerRun = 60

// Run implements jobs.Handler: AniList's relations first (they need no Wikidata), then Wikidata's franchises for the next
// series, then the reconciliation. A failing query ends its stage to be tried again later.
func (p FranchisePass) Run(ctx context.Context, _ jobs.Job) error {
	if err := p.readAnime(ctx); err != nil {
		return err
	}
	found, err := p.readWikidata(ctx)
	if err != nil {
		return err
	}
	changed, err := p.Reconcile(ctx)
	if err != nil {
		return err
	}
	p.Logger.Info("franchise pass finished", slog.Int("franchises", found), slog.Int("collections changed", changed))
	return nil
}

// readAnime walks AniList's relations from each anime entry not read yet and records the library's series it reaches.
func (p FranchisePass) readAnime(ctx context.Context) error {
	if p.Anime == nil {
		return nil
	}
	entries, err := p.Franchises.AnimeWithoutRelationCheck(ctx, animePerRun)
	if err != nil {
		return err
	}
	done := map[int64]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.franchises.context", string(FranchisePassJobType), err)
		}
		if !p.relateAnime(ctx, entry) {
			break
		}
		done[entry.WorkID] = true
	}
	for workID := range done {
		if err := p.Franchises.MarkAnimeChecked(ctx, workID); err != nil {
			return err
		}
	}
	return nil
}

// relateAnime walks AniList's relations from one entry a few steps and records the library's series it reaches; false
// says AniList failed and the run should stop.
func (p FranchisePass) relateAnime(ctx context.Context, entry library.UncheckedAnime) bool {
	seen := map[int64]bool{entry.AniList: true}
	queue := []int64{entry.AniList}
	for calls := 0; len(queue) > 0 && calls < animeCrawlBudget; calls++ {
		id := queue[0]
		queue = queue[1:]
		related, err := p.Anime.Related(ctx, id)
		if err != nil {
			p.Logger.Warn("anime relations not read", slog.Int64("anilist", id), slog.Any("error", err))
			return errors.Is(err, anilist.ErrNotFound)
		}
		for _, relation := range related {
			if !relatedTypes[relation.Type] || seen[relation.ID] {
				continue
			}
			seen[relation.ID] = true
			queue = append(queue, relation.ID)
			if workID, found, err := p.Series.WorkByProviderID(ctx, ProviderAniList, strconv.FormatInt(relation.ID, 10)); err == nil && found && workID != entry.WorkID {
				if err := p.Franchises.Relate(ctx, entry.WorkID, workID); err != nil {
					p.Logger.Warn("relation not kept", slog.Any("error", err))
				}
			}
		}
	}
	return true
}

// franchisesPerRun bounds one Wikidata stage: Wikimedia asks for polite use, so a library is worked off over several runs.
const franchisesPerRun = 60

// readWikidata asks Wikidata for the franchise of the next series and puts the series the library holds in it.
func (p FranchisePass) readWikidata(ctx context.Context) (int, error) {
	unchecked, err := p.Franchises.SeriesWithoutFranchiseCheck(ctx, franchisesPerRun)
	if err != nil {
		return 0, err
	}
	found := 0
	for _, series := range unchecked {
		if err := ctx.Err(); err != nil {
			return found, jobs.Retryablef("tv.franchises.context", string(FranchisePassJobType), err)
		}
		franchise, ok, err := p.Source.SeriesFranchise(ctx, series.TVDB, series.TMDB)
		if err != nil {
			p.Logger.Warn("franchise lookup failed", slog.Int64("work_id", series.WorkID), slog.Any("error", err))
			break
		}
		if !ok {
			if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
				return found, err
			}
			continue
		}
		members, err := p.Source.FranchiseSeriesOf(ctx, franchise.QID)
		if err != nil {
			p.Logger.Warn("franchise members not read", slog.String("franchise", franchise.QID), slog.Any("error", err))
			break
		}
		held := p.heldWorks(ctx, members)
		if len(members) >= 2 {
			held[series.WorkID] = true
			var id int64
			for workID := range held {
				if id, err = p.Franchises.Join(ctx, MediaType, ProviderWikidata, franchise.QID, franchise.Label, workID); err != nil {
					return found, err
				}
			}
			if err := p.Franchises.SetTotal(ctx, id, int64(len(members))); err != nil {
				return found, err
			}
			found++
		}
		for workID := range held {
			if err := p.Franchises.MarkChecked(ctx, workID); err != nil {
				return found, err
			}
		}
		if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
			return found, err
		}
	}
	return found, nil
}

// heldWorks is the library's work for each member it holds, found by TheTVDB or TMDB id.
func (p FranchisePass) heldWorks(ctx context.Context, members []wikidata.FranchiseSeries) map[int64]bool {
	held := map[int64]bool{}
	for _, member := range members {
		if workID, ok := heldMember(ctx, p.Series, member); ok {
			held[workID] = true
		}
	}
	return held
}

func heldMember(ctx context.Context, series library.Series, member wikidata.FranchiseSeries) (int64, bool) {
	if member.TVDB > 0 {
		if id, found, err := series.WorkByProviderID(ctx, ProviderTVDB, strconv.FormatInt(member.TVDB, 10)); err == nil && found {
			return id, true
		}
	}
	if member.TMDB > 0 {
		if id, found, err := series.WorkByProviderID(ctx, ProviderTMDB, strconv.FormatInt(member.TMDB, 10)); err == nil && found {
			return id, true
		}
	}
	return 0, false
}

// Reconcile gathers everything that says series belong together (the franchises already kept, names, AniList's
// relations, crossover episodes, a creator and a network in common) and makes each family one collection: the
// franchise a provider named when there is one, its duplicates merged into it, named after what is known of it.
func (p FranchisePass) Reconcile(ctx context.Context) (int, error) {
	works, err := p.Franchises.WorkNames(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	traits, err := p.Franchises.WorkTraits(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	var edges [][2]int64
	for _, edge := range titleEdges(works) {
		if compatible(traits[edge[0]], traits[edge[1]]) {
			edges = append(edges, edge)
		}
	}
	edges = append(edges, sameMakers(traits)...)
	related, err := p.Franchises.RelatedPairs(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	edges = append(edges, related...)
	crossovers, err := p.Crossovers.Pairs(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	for _, pair := range crossovers {
		edges = append(edges, [2]int64{pair.WorkID, pair.OtherWorkID})
	}
	unrelated, err := p.Franchises.UnrelatedPairs(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	collections, err := p.Franchises.Summaries(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	memberships, err := p.Franchises.FranchiseMemberships(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, plan := range planFranchises(works, collections, memberships, edges, unrelated) {
		keep := plan.Keep
		if keep == 0 {
			if keep, err = p.Franchises.CreateAuto(ctx, MediaType, plan.Name); err != nil {
				return changed, err
			}
		}
		if err := p.Franchises.MergeFranchises(ctx, keep, plan.Merge); err != nil {
			return changed, err
		}
		if err := p.Franchises.AddToFranchise(ctx, keep, plan.Members); err != nil {
			return changed, err
		}
		if plan.Rename {
			if err := p.Franchises.SetFranchiseName(ctx, keep, plan.Name); err != nil {
				return changed, err
			}
		}
		changed++
	}
	return changed, nil
}

// EnqueueFranchisePass submits one pass; a second while one waits is the same work.
func EnqueueFranchisePass(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: FranchisePassJobType, ActionKey: string(FranchisePassJobType), Priority: jobs.PriorityLow})
}

// Franchising is TV's part of a series' collections: the series Wikidata lists in a franchise that the library lacks,
// and following one, which adds them watching only what airs next.
type Franchising struct {
	Source           FranchiseSource
	Franchises       library.Franchises
	Series           library.Series
	Service          Service
	DefaultProfileID int64
	Categories       interface {
		Category(id int64) (settings.Category, bool)
		ProfileFor(category settings.Category, fallback int64) int64
	}
	// Pictures keeps the pictures found for series the library lacks; nil asks every time.
	Pictures *MissingPictures
}

var _ media.Franchising = Franchising{}

func (f Franchising) missing(ctx context.Context, franchiseID int64) ([]wikidata.FranchiseSeries, []wikidata.FranchiseSeries, library.FranchiseRow, error) {
	row, err := f.Franchises.Get(ctx, franchiseID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, row, media.ErrNoFranchise
	}
	if err != nil || f.Source == nil || row.Kind != library.FranchiseKind {
		return nil, nil, row, err
	}
	// A franchise merged from several Wikidata items lists the series of each.
	var members []wikidata.FranchiseSeries
	seen := map[string]bool{}
	for _, id := range row.ExternalIDs {
		qid, isItem := strings.CutPrefix(id, ProviderWikidata+":")
		if !isItem {
			continue
		}
		listed, err := f.Source.FranchiseSeriesOf(ctx, qid)
		if err != nil {
			return nil, nil, row, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
		}
		for _, member := range listed {
			if !seen[member.QID] {
				seen[member.QID] = true
				members = append(members, member)
			}
		}
	}
	var absent []wikidata.FranchiseSeries
	for index, member := range members {
		if member.TVDB == 0 && member.TMDB == 0 {
			members[index] = f.resolve(ctx, member)
			member = members[index]
		}
		if _, ok := heldMember(ctx, f.Series, member); !ok && (member.TVDB > 0 || member.TMDB > 0) {
			absent = append(absent, member)
		}
	}
	return members, absent, row, nil
}

// MissingFranchiseMembers implements media.Franchising.
func (f Franchising) MissingFranchiseMembers(ctx context.Context, _, franchiseID int64) ([]media.FranchiseMember, error) {
	_, absent, _, err := f.missing(ctx, franchiseID)
	if err != nil {
		return nil, err
	}
	out := make([]media.FranchiseMember, len(absent))
	for index, member := range absent {
		out[index] = media.FranchiseMember{Title: member.Label, Year: member.Year, Provider: ProviderTVDB, ExternalID: strconv.FormatInt(member.TVDB, 10)}
		if member.TVDB == 0 {
			out[index].Provider, out[index].ExternalID = ProviderTMDB, strconv.FormatInt(member.TMDB, 10)
		}
		out[index].PosterURL = f.poster(ctx, member)
	}
	return out, nil
}

// FranchiseFacts implements media.Franchising: Wikidata has no pictures, so a franchise shows its first series'; the count of
// its series is kept by the pass that reads them.
func (Franchising) FranchiseFacts(context.Context, []media.FranchiseRef) map[int64]media.FranchiseFact {
	return nil
}

// MonitorFranchise implements media.Franchising: following adds every series of the franchise the library lacks.
func (f Franchising) MonitorFranchise(ctx context.Context, categoryID, franchiseID int64, monitored bool) (int, error) {
	added := 0
	var total int64
	if monitored {
		members, absent, _, err := f.missing(ctx, franchiseID)
		if err != nil {
			return 0, err
		}
		total = int64(len(members))
		category, _ := f.Categories.Category(categoryID)
		profile := f.Categories.ProfileFor(category, f.DefaultProfileID)
		for _, member := range absent {
			ids := IDs{TVDB: member.TVDB}
			if member.TVDB == 0 {
				ids = IDs{TMDB: member.TMDB}
			}
			if _, err := f.Service.Add(ctx, AddRequest{IDs: ids, CategoryID: categoryID, ProfileID: profile, Monitor: MonitorFuture}); err != nil {
				return added, fmt.Errorf("add %q: %w", member.Label, err)
			}
			added++
		}
	}
	if err := f.Franchises.SetMonitored(ctx, franchiseID, monitored, total); err != nil {
		return added, err
	}
	return added, nil
}

// MissingPictures remembers what was found for series the library lacks, so each is asked for once: its picture by provider
// id, and the TMDB id of a series Wikidata names without one.
type MissingPictures struct {
	mu       sync.Mutex
	posters  map[string]string
	resolved map[string]int64
}

// NewMissingPictures builds an empty cache.
func NewMissingPictures() *MissingPictures {
	return &MissingPictures{posters: map[string]string{}, resolved: map[string]int64{}}
}

func (f Franchising) poster(ctx context.Context, member wikidata.FranchiseSeries) string {
	if f.Pictures == nil || f.Service.TMDB == nil || !f.Service.TMDB.Configured() {
		return ""
	}
	key := strconv.FormatInt(member.TVDB, 10) + "/" + strconv.FormatInt(member.TMDB, 10)
	f.Pictures.mu.Lock()
	cached, known := f.Pictures.posters[key]
	asked := len(f.Pictures.posters)
	f.Pictures.mu.Unlock()
	if known || asked >= 5000 {
		return cached
	}
	var path string
	switch {
	case member.TMDB > 0:
		if series, err := f.Service.TMDB.Series(ctx, member.TMDB); err == nil {
			path = series.PosterPath
		}
	case member.TVDB > 0:
		if hit, err := f.Service.TMDB.FindByTVDB(ctx, member.TVDB); err == nil {
			path = hit.PosterPath
		}
	}
	poster := tmdb.ImageURL(path)
	f.Pictures.mu.Lock()
	f.Pictures.posters[key] = poster
	f.Pictures.mu.Unlock()
	return poster
}

// resolve finds the TMDB id of a series Wikidata names but has no TheTVDB or TMDB id for, by its name and first year: the
// original PLUH has none there. A name that matches nothing closely stays unresolved.
func (f Franchising) resolve(ctx context.Context, member wikidata.FranchiseSeries) wikidata.FranchiseSeries {
	if f.Pictures == nil || f.Service.TMDB == nil || !f.Service.TMDB.Configured() || member.Label == "" {
		return member
	}
	f.Pictures.mu.Lock()
	id, known := f.Pictures.resolved[member.QID]
	f.Pictures.mu.Unlock()
	if !known {
		if hits, err := f.Service.TMDB.Search(ctx, member.Label); err == nil {
			for _, hit := range hits {
				year := 0
				if len(hit.FirstAirDate) >= 4 {
					year, _ = strconv.Atoi(hit.FirstAirDate[:4])
				}
				if slices.Equal(words(hit.Name), words(member.Label)) && (member.Year == 0 || year == 0 || max(member.Year-year, year-member.Year) <= 1) {
					id = hit.ID
					break
				}
			}
		}
		f.Pictures.mu.Lock()
		f.Pictures.resolved[member.QID] = id
		f.Pictures.mu.Unlock()
	}
	member.TMDB = id
	return member
}
