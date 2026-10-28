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
	"time"

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

// FranchiseSource is Wikidata as the franchise pass asks it, many series at a time: what ties each series to others, and
// the series of each franchise.
type FranchiseSource interface {
	TiesOf(ctx context.Context, series []wikidata.SeriesRef) (map[wikidata.SeriesRef]wikidata.SeriesTies, error)
	FranchiseMembers(ctx context.Context, qids []string) (map[string][]wikidata.FranchiseSeries, error)
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
	// Again asks for another pass soon, when AniList's time ran out with entries still unread; nil asks none.
	Again func(ctx context.Context)
}

// relatedTypes are the AniList relations that put two entries in one franchise.
var relatedTypes = map[string]bool{"SEQUEL": true, "PREQUEL": true, "SPIN_OFF": true, "SIDE_STORY": true, "PARENT": true, "ALTERNATIVE": true}

// animeCrawlBudget bounds the AniList calls one entry's relations may take.
const animeCrawlBudget = 12

// animePerRun bounds the anime entries one run reads relations of.
const animePerRun = 60

// animeTime bounds AniList's stage: it is slow (one call per two seconds), and the collections wait for it.
const animeTime = 3 * time.Minute

// Run implements jobs.Handler: Wikidata for the whole library, the reconciliation and what each collection lacks, then
// AniList's relations for a few minutes and the reconciliation once more. A failing source ends its stage only.
func (p FranchisePass) Run(ctx context.Context, _ jobs.Job) error {
	known, err := p.readWikidata(ctx)
	if err != nil {
		return err
	}
	changed, err := p.Reconcile(ctx, known)
	if err != nil {
		return err
	}
	if known != nil {
		if err := p.countFamilies(ctx, *known); err != nil {
			return err
		}
	}
	if err := p.readAnime(ctx); err != nil {
		return err
	}
	more, err := p.Reconcile(ctx, known)
	if err != nil {
		return err
	}
	p.Logger.Info("franchise pass finished", slog.Int("collections changed", changed+more))
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
	budget, stop := context.WithTimeout(ctx, animeTime)
	defer stop()
	done := map[int64]bool{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("tv.franchises.context", string(FranchisePassJobType), err)
		}
		if budget.Err() != nil || !p.relateAnime(budget, entry) {
			break
		}
		done[entry.WorkID] = true
	}
	for workID := range done {
		if err := p.Franchises.MarkAnimeChecked(ctx, workID); err != nil {
			return err
		}
	}
	// A pass that made progress and ran out of time is followed by another soon, so a new library's anime are read in an
	// hour rather than over days of the schedule.
	unread := len(done) < len(entries) || len(entries) == animePerRun
	if p.Again != nil && len(done) > 0 && unread {
		p.Again(ctx)
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
			if workID, found, err := p.Series.WorkByProviderID(ctx, MediaType, ProviderAniList, strconv.FormatInt(relation.ID, 10)); err == nil && found && workID != entry.WorkID {
				if err := p.Franchises.Relate(ctx, entry.WorkID, workID); err != nil {
					p.Logger.Warn("relation not kept", slog.Any("error", err))
				}
			}
		}
	}
	return true
}

// wikidataKnowledge is what one run learnt from Wikidata: the ties of every series it knows, and the series of each franchise.
type wikidataKnowledge struct {
	refs    map[int64]wikidata.SeriesRef
	ties    map[wikidata.SeriesRef]wikidata.SeriesTies
	members map[string][]wikidata.FranchiseSeries
	// listed is the series of the library each Wikidata collection holds by Wikidata's own word, by collection.
	listed map[int64][]int64
}

// readWikidata asks Wikidata about every series at once (a few queries for a whole library): the franchises it names
// become collections of the series the library holds, and the spin-offs and sequels among them evidence. A failing
// query ends the stage, nil knowledge, to be tried again next run.
func (p FranchisePass) readWikidata(ctx context.Context) (*wikidataKnowledge, error) {
	if p.Source == nil {
		return nil, nil
	}
	series, err := p.Franchises.SeriesIDs(ctx)
	if err != nil {
		return nil, err
	}
	unrelated, err := p.Franchises.UnrelatedPairs(ctx, MediaType)
	if err != nil {
		return nil, err
	}
	known := wikidataKnowledge{refs: map[int64]wikidata.SeriesRef{}, listed: map[int64][]int64{}}
	byRef := map[wikidata.SeriesRef]int64{}
	refs := make([]wikidata.SeriesRef, 0, len(series))
	for _, one := range series {
		ref := wikidata.SeriesRef{TVDB: one.TVDB, TMDB: one.TMDB}
		if ref.TVDB == 0 && ref.TMDB == 0 {
			continue
		}
		known.refs[one.WorkID], byRef[ref] = ref, one.WorkID
		refs = append(refs, ref)
	}
	if known.ties, err = p.Source.TiesOf(ctx, refs); err != nil {
		p.Logger.Warn("wikidata ties not read", slog.Any("error", err))
		return nil, nil
	}
	held := heldIndex(series)
	var qids []string
	for ref, ties := range known.ties {
		workID := byRef[ref]
		for _, related := range ties.Related {
			if other, ok := held.of(related); ok && other != workID && !unrelated[[2]int64{workID, other}] {
				if err := p.Franchises.Relate(ctx, workID, other); err != nil {
					return nil, err
				}
			}
		}
		for _, franchise := range ties.Franchises {
			if !slices.Contains(qids, franchise.QID) {
				qids = append(qids, franchise.QID)
			}
		}
	}
	slices.Sort(qids)
	if err := p.relateFranchiseParts(ctx, known.ties, byRef, qids); err != nil {
		return nil, err
	}
	if known.members, err = p.Source.FranchiseMembers(ctx, qids); err != nil {
		p.Logger.Warn("wikidata franchises not read", slog.Any("error", err))
		return nil, nil
	}
	labels := map[string]string{}
	for _, ties := range known.ties {
		for _, franchise := range ties.Franchises {
			labels[franchise.QID] = franchise.Label
		}
	}
	for _, qid := range qids {
		members := known.members[qid]
		// An item that is itself a series is a programme strand the others are part of, not a franchise to name.
		if strand := slices.IndexFunc(members, func(member wikidata.FranchiseSeries) bool { return member.QID == qid }); strand >= 0 {
			p.relateToStrand(ctx, known, held, members[strand], qid, unrelated)
			continue
		}
		var inLibrary []int64
		for _, member := range members {
			if workID, ok := held.of(member); ok && !slices.Contains(inLibrary, workID) {
				inLibrary = append(inLibrary, workID)
			}
		}
		// A franchise of one series is no family; Wikidata lists the series itself among its own.
		if len(members) < 2 || len(inLibrary) == 0 {
			continue
		}
		for _, workID := range inLibrary {
			// A series a person took out of the franchise stays out.
			if slices.ContainsFunc(inLibrary, func(other int64) bool { return unrelated[[2]int64{workID, other}] }) {
				continue
			}
			id, err := p.Franchises.Join(ctx, MediaType, ProviderWikidata, qid, labels[qid], workID)
			if err != nil {
				return nil, err
			}
			if id != 0 {
				known.listed[id] = append(known.listed[id], workID)
			}
		}
	}
	return &known, nil
}

// relateFranchiseParts ties a series of a franchise to one of the franchise it is part of, when the library has series of
// both: a phase of a cinematic universe and the universe.
func (p FranchisePass) relateFranchiseParts(ctx context.Context, ties map[wikidata.SeriesRef]wikidata.SeriesTies, byRef map[wikidata.SeriesRef]int64, qids []string) error {
	seriesOf := map[string]int64{}
	for ref, one := range ties {
		for _, franchise := range one.Franchises {
			if _, seen := seriesOf[franchise.QID]; !seen {
				seriesOf[franchise.QID] = byRef[ref]
			}
		}
	}
	for ref, one := range ties {
		for _, franchise := range one.Franchises {
			for _, parent := range franchise.Parents {
				if other, ok := seriesOf[parent]; ok && slices.Contains(qids, parent) && other != byRef[ref] {
					if err := p.Franchises.Relate(ctx, byRef[ref], other); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// relateToStrand ties the library's series that are part of a strand to the strand itself, when the library holds it.
func (p FranchisePass) relateToStrand(ctx context.Context, known wikidataKnowledge, held heldSeries, strand wikidata.FranchiseSeries, qid string, unrelated map[[2]int64]bool) {
	strandID, ok := held.of(strand)
	if !ok {
		return
	}
	for ref, ties := range known.ties {
		workID, isHeld := held.of(wikidata.FranchiseSeries{TVDB: ref.TVDB, TMDB: ref.TMDB})
		if !isHeld || workID == strandID || unrelated[[2]int64{workID, strandID}] || !containsFranchise(ties.Franchises, qid) {
			continue
		}
		if err := p.Franchises.Relate(ctx, workID, strandID); err != nil {
			p.Logger.Warn("relation not kept", slog.Any("error", err))
		}
	}
}

func containsFranchise(franchises []wikidata.Franchise, qid string) bool {
	return slices.ContainsFunc(franchises, func(franchise wikidata.Franchise) bool { return franchise.QID == qid })
}

// heldSeries finds the library's series by the TheTVDB or TMDB id Wikidata gives.
type heldSeries struct{ tvdb, tmdb map[int64]int64 }

func heldIndex(series []library.SeriesIDs) heldSeries {
	index := heldSeries{tvdb: map[int64]int64{}, tmdb: map[int64]int64{}}
	for _, one := range series {
		if one.TVDB > 0 {
			index.tvdb[one.TVDB] = one.WorkID
		}
		if one.TMDB > 0 {
			index.tmdb[one.TMDB] = one.WorkID
		}
	}
	return index
}

func (h heldSeries) of(member wikidata.FranchiseSeries) (int64, bool) {
	if id, ok := h.tvdb[member.TVDB]; ok && member.TVDB > 0 {
		return id, true
	}
	id, ok := h.tmdb[member.TMDB]
	return id, ok && member.TMDB > 0
}

// familySeries is every series Wikidata ties to the given ones, once each: the series of their franchises (and of the
// franchises named besides) and those they are spin-offs or sequels of.
func familySeries(refs []wikidata.SeriesRef, franchises []string, ties map[wikidata.SeriesRef]wikidata.SeriesTies, members map[string][]wikidata.FranchiseSeries) []wikidata.FranchiseSeries {
	var out []wikidata.FranchiseSeries
	seen := map[string]bool{}
	add := func(series wikidata.FranchiseSeries) {
		if !seen[series.QID] {
			seen[series.QID] = true
			out = append(out, series)
		}
	}
	qids := slices.Clone(franchises)
	for _, ref := range refs {
		for _, franchise := range ties[ref].Franchises {
			qids = append(qids, franchise.QID)
		}
		for _, related := range ties[ref].Related {
			add(related)
		}
	}
	for _, qid := range qids {
		strand := slices.ContainsFunc(members[qid], func(member wikidata.FranchiseSeries) bool { return member.QID == qid })
		if len(members[qid]) < 2 || strand {
			continue
		}
		for _, member := range members[qid] {
			add(member)
		}
	}
	return out
}

// countFamilies keeps how many series each collection has, those the library lacks included, as Wikidata knows them.
func (p FranchisePass) countFamilies(ctx context.Context, known wikidataKnowledge) error {
	series, err := p.Franchises.SeriesIDs(ctx)
	if err != nil {
		return err
	}
	held := heldIndex(series)
	collections, err := p.Franchises.Summaries(ctx, MediaType)
	if err != nil {
		return err
	}
	memberships, err := p.Franchises.FranchiseMemberships(ctx, MediaType)
	if err != nil {
		return err
	}
	membersOf := map[int64][]int64{}
	for _, membership := range memberships {
		membersOf[membership.CollectionID] = append(membersOf[membership.CollectionID], membership.WorkID)
	}
	for _, collection := range collections {
		inLibrary := membersOf[collection.ID]
		if len(inLibrary) == 0 {
			continue
		}
		var refs []wikidata.SeriesRef
		for _, workID := range inLibrary {
			refs = append(refs, known.refs[workID])
		}
		total := len(inLibrary)
		for _, series := range familySeries(refs, wikidataItems(collection.ExternalIDs), known.ties, known.members) {
			if workID, ok := held.of(series); !ok || !slices.Contains(inLibrary, workID) {
				total++
			}
		}
		if err := p.Franchises.SetTotal(ctx, collection.ID, int64(total)); err != nil {
			return err
		}
	}
	return nil
}

// wikidataItems are the Wikidata items among a collection's ids.
func wikidataItems(externalIDs []string) []string {
	var qids []string
	for _, id := range externalIDs {
		if qid, isItem := strings.CutPrefix(id, ProviderWikidata+":"); isItem {
			qids = append(qids, qid)
		}
	}
	return qids
}

// Reconcile gathers everything that says series belong together (providers' franchises, names, AniList, crossover
// episodes, shared characters) and makes each family one collection; one Solarr made whose evidence is gone goes with it.
func (p FranchisePass) Reconcile(ctx context.Context, known *wikidataKnowledge) (int, error) {
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
	characters, err := p.Franchises.CharacterCredits(ctx, MediaType)
	if err != nil {
		return 0, err
	}
	for _, edge := range characterEdges(characters, works) {
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
	// One broadcast two unlike series share is a special about one, not a family: "Talking ..." under a documentary strand;
	// a broadcaster in common says nothing there.
	broadcasts := map[[2]int64]map[[2]int64]bool{}
	for _, pair := range crossovers {
		works := [2]int64{min(pair.WorkID, pair.OtherWorkID), max(pair.WorkID, pair.OtherWorkID)}
		if broadcasts[works] == nil {
			broadcasts[works] = map[[2]int64]bool{}
		}
		broadcasts[works][[2]int64{min(pair.PartID, pair.OtherPartID), max(pair.PartID, pair.OtherPartID)}] = true
	}
	for pair, shared := range broadcasts {
		if len(shared) >= 2 || sameKind(traits[pair[0]], traits[pair[1]]) {
			edges = append(edges, pair)
		}
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
	var listed map[int64][]int64
	if known != nil {
		listed = known.listed
	}
	plans, drop := planFranchises(works, collections, memberships, listed, edges, unrelated)
	changed := 0
	for _, plan := range plans {
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
		if err := p.Franchises.RemoveFromFranchise(ctx, keep, plan.Remove); err != nil {
			return changed, err
		}
		if plan.Rename {
			if err := p.Franchises.SetFranchiseName(ctx, keep, plan.Name); err != nil {
				return changed, err
			}
		}
		changed++
	}
	for _, id := range drop {
		if err := p.Franchises.DropFranchise(ctx, id); err != nil {
			return changed, err
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
	inLibrary, err := f.Franchises.MembersOf(ctx, franchiseID)
	if err != nil {
		return nil, nil, row, err
	}
	series, err := f.Franchises.SeriesIDs(ctx)
	if err != nil {
		return nil, nil, row, err
	}
	var refs []wikidata.SeriesRef
	for _, one := range series {
		if slices.Contains(inLibrary, one.WorkID) && (one.TVDB > 0 || one.TMDB > 0) {
			refs = append(refs, wikidata.SeriesRef{TVDB: one.TVDB, TMDB: one.TMDB})
		}
	}
	ties, err := f.Source.TiesOf(ctx, refs)
	if err != nil {
		return nil, nil, row, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	qids := wikidataItems(row.ExternalIDs)
	for _, one := range ties {
		for _, franchise := range one.Franchises {
			qids = append(qids, franchise.QID)
		}
	}
	members, err := f.Source.FranchiseMembers(ctx, qids)
	if err != nil {
		return nil, nil, row, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	known := familySeries(refs, wikidataItems(row.ExternalIDs), ties, members)
	held := heldIndex(series)
	var absent []wikidata.FranchiseSeries
	for _, member := range known {
		if _, ok := held.of(member); !ok {
			absent = append(absent, member)
		}
	}
	return known, absent, row, nil
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

// MissingPictures remembers the pictures found for series the library lacks, so each is asked for once.
type MissingPictures struct {
	mu      sync.Mutex
	posters map[string]string
}

// NewMissingPictures builds an empty cache.
func NewMissingPictures() *MissingPictures {
	return &MissingPictures{posters: map[string]string{}}
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
