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

// franchisesPerRun bounds one pass: Wikimedia asks for polite use of its query service, so a library is worked off over days.
const franchisesPerRun = 60

// AnimeRelations is AniList as the franchise pass asks it: what an anime is a sequel, prequel or spin-off of.
type AnimeRelations interface {
	Related(ctx context.Context, id int64) ([]anilist.Relation, error)
}

// FranchisePass puts each series in the franchise Wikidata says it is part of, with the other series of it the library
// holds; anime go by AniList's relations, and series whose names or crossover episodes say they belong together are
// grouped by Solarr itself.
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

// animeCrawlBudget bounds the AniList calls one series' relations may take.
const animeCrawlBudget = 12

// Run implements jobs.Handler: it asks about the next series; a failing query ends the run to be tried again later.
func (p FranchisePass) Run(ctx context.Context, _ jobs.Job) error {
	unchecked, err := p.Franchises.SeriesWithoutFranchiseCheck(ctx, franchisesPerRun)
	if err != nil {
		return err
	}
	found := 0
	for _, series := range unchecked {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.franchises.context", string(FranchisePassJobType), err)
		}
		p.relateAnime(ctx, series)
		franchise, ok, err := p.Source.SeriesFranchise(ctx, series.TVDB, series.TMDB)
		if err != nil {
			p.Logger.Warn("franchise lookup failed", slog.Int64("work_id", series.WorkID), slog.Any("error", err))
			break
		}
		if !ok {
			if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
				return err
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
			for workID := range held {
				if _, err := p.Franchises.Join(ctx, MediaType, ProviderWikidata, franchise.QID, franchiseName(franchise.Label, members), workID); err != nil {
					return err
				}
			}
			found++
		}
		for workID := range held {
			if err := p.Franchises.MarkChecked(ctx, workID); err != nil {
				return err
			}
		}
		if err := p.Franchises.MarkChecked(ctx, series.WorkID); err != nil {
			return err
		}
	}
	grouped, err := p.groupByName(ctx)
	if err != nil {
		return err
	}
	p.Logger.Info("franchise pass finished", slog.Int("asked", len(unchecked)), slog.Int("franchises", found), slog.Int("grouped", grouped))
	return nil
}

// relateAnime walks AniList's relations from the series' entry a few steps and records the library's series it reaches.
func (p FranchisePass) relateAnime(ctx context.Context, series library.UncheckedSeries) {
	if p.Anime == nil || series.AniList == 0 {
		return
	}
	seen := map[int64]bool{series.AniList: true}
	queue := []int64{series.AniList}
	for calls := 0; len(queue) > 0 && calls < animeCrawlBudget; calls++ {
		id := queue[0]
		queue = queue[1:]
		related, err := p.Anime.Related(ctx, id)
		if err != nil {
			p.Logger.Warn("anime relations not read", slog.Int64("anilist", id), slog.Any("error", err))
			return
		}
		for _, relation := range related {
			if !relatedTypes[relation.Type] || seen[relation.ID] {
				continue
			}
			seen[relation.ID] = true
			queue = append(queue, relation.ID)
			if workID, found, err := p.Series.WorkByProviderID(ctx, ProviderAniList, strconv.FormatInt(relation.ID, 10)); err == nil && found && workID != series.WorkID {
				if err := p.Franchises.Relate(ctx, series.WorkID, workID); err != nil {
					p.Logger.Warn("relation not kept", slog.Any("error", err))
				}
			}
		}
	}
}

// groupByName puts together the series that names, AniList's relations or crossover episodes say belong together, unless a
// franchise a provider named already holds them all; it answers how many groups it kept.
func (p FranchisePass) groupByName(ctx context.Context) (int, error) {
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
	memberships, err := p.Franchises.FranchiseMemberships(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	titles := make(map[int64]string, len(works))
	for _, work := range works {
		titles[work.WorkID] = work.Title
	}
	kept := 0
	for _, group := range groupsOf(edges) {
		if heldTogether(memberships, group) {
			continue
		}
		// A franchise a provider named that already holds some of them takes the rest in, so one family is one collection.
		if id := mostOverlapping(memberships, group); id != 0 {
			if err := p.Franchises.AddToFranchise(ctx, id, group); err != nil {
				return kept, err
			}
			kept++
			continue
		}
		if _, err := p.Franchises.JoinGroup(ctx, MediaType, groupName(titles, group), group, memberships); err != nil {
			return kept, err
		}
		kept++
	}
	return kept, nil
}

// heldTogether says one franchise a provider named already holds every work of the group.
func heldTogether(memberships []library.Membership, group []int64) bool {
	count := map[int64]int{}
	for _, membership := range memberships {
		if membership.Source != library.AutoSource && slices.Contains(group, membership.WorkID) {
			count[membership.CollectionID]++
		}
	}
	for _, held := range count {
		if held == len(group) {
			return true
		}
	}
	return false
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
	if err != nil || f.Source == nil || row.ExternalID == "" || row.Kind != library.FranchiseKind {
		return nil, nil, row, err
	}
	members, err := f.Source.FranchiseSeriesOf(ctx, row.ExternalID)
	if err != nil {
		return nil, nil, row, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	var absent []wikidata.FranchiseSeries
	for _, member := range members {
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

// FranchisePictures implements media.Franchising: Wikidata has none, so a franchise shows its first series' picture.
func (Franchising) FranchisePictures(context.Context, []media.FranchiseRef) map[int64]string {
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

// MissingPictures remembers the pictures found for series the library lacks, by provider id, so each is asked for once.
type MissingPictures struct {
	mu      sync.Mutex
	posters map[string]string
}

// NewMissingPictures builds an empty cache.
func NewMissingPictures() *MissingPictures { return &MissingPictures{posters: map[string]string{}} }

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

// mostOverlapping is the franchise a provider named that holds the most of the group, 0 when none holds any.
func mostOverlapping(memberships []library.Membership, group []int64) int64 {
	count := map[int64]int{}
	for _, membership := range memberships {
		if membership.Source != library.AutoSource && slices.Contains(group, membership.WorkID) {
			count[membership.CollectionID]++
		}
	}
	var best int64
	for id, held := range count {
		if held > count[best] || (held == count[best] && id < best) {
			best = id
		}
	}
	return best
}

// franchiseName is the name a provider's franchise goes by: the franchise item's own label, unless that is one series of
// it ("Hik 10: Ihaol Force" for Hik 10), when what the series share is.
func franchiseName(label string, members []wikidata.FranchiseSeries) string {
	titles := map[int64]string{}
	ids := make([]int64, 0, len(members))
	named := false
	for index, member := range members {
		titles[int64(index)] = member.Label
		ids = append(ids, int64(index))
		named = named || member.Label == label
	}
	if !named || len(members) < 2 {
		return label
	}
	if common := groupName(titles, ids); common != "" && !strings.Contains(common, " / ") && len(common) < len(label) {
		return common
	}
	return label
}
