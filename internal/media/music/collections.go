package music

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

const (
	// ProviderWikidata names a music collection by the Wikidata item of its franchise or series.
	ProviderWikidata = "wikidata"
	// ProviderMusicBrainzSeries names a music collection by the MusicBrainz series it is; also the source asked once per album.
	ProviderMusicBrainzSeries = "musicbrainz-series"
	// releaseGroupSeriesType is the MusicBrainz series type whose members are albums.
	releaseGroupSeriesType = "Release group series"
	// soundtrackType is the MusicBrainz secondary type of an album that is a score or soundtrack.
	soundtrackType = "Soundtrack"
	// compilationType is the secondary type of a compilation, which MusicBrainz also files in release group series.
	compilationType = "Compilation"
	// seriesLookupsPerRun bounds the MusicBrainz requests one pass makes for series (one a second, shared with enrichment).
	seriesLookupsPerRun = 120
	// seriesScanPerRun bounds the albums one pass looks at for series, most of them answered from the cache.
	seriesScanPerRun = 2000
)

// CollectionPassJobType is the registered type of the pass that puts albums in the series and franchises they belong to.
const CollectionPassJobType = jobs.Type("music.collections")

// CollectionPassDefinition declares the pass: background metadata work, retried when Wikidata is busy.
var CollectionPassDefinition = jobs.Definition{
	Name: "Finding the series and franchises albums belong to",
	Type: CollectionPassJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// CollectionSource is Wikidata as the pass asks it, many albums at a time.
type CollectionSource interface {
	AlbumFranchises(ctx context.Context, releaseGroups []string) (map[string][]wikidata.AlbumFranchise, error)
	MusicSeriesItems(ctx context.Context, seriesMBIDs []string) (map[string]string, error)
	ItemFranchises(ctx context.Context, qids []string) (map[string][]string, error)
}

// SeriesSource is MusicBrainz as the pass asks it: an album's types from the cache, and the series it is part of.
type SeriesSource interface {
	Album(ctx context.Context, priority musicbrainz.Priority, releaseGroupMBID, releaseMBID string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error)
	ReleaseGroupSeries(ctx context.Context, priority musicbrainz.Priority, releaseGroupMBID string) ([]musicbrainz.Series, bool, error)
}

// CollectionPass keeps music collections a provider names: the MusicBrainz series a soundtrack is part of, and the
// franchise Wikidata puts an album in or the film it scores, which carries the keys that link it with the films, series
// and books of that franchise.
type CollectionPass struct {
	// Wikidata nil reads no franchises; MusicBrainz nil reads no series.
	Wikidata    CollectionSource
	MusicBrainz SeriesSource
	// Libraries puts each album's lookup in its library's provider order; nil takes the default.
	Libraries  Libraries
	Franchises library.Franchises
	Logger     *slog.Logger
}

// Run implements jobs.Handler: MusicBrainz's series within its budget, then Wikidata's franchises for every album.
func (p CollectionPass) Run(ctx context.Context, _ jobs.Job) error {
	if err := p.readSeries(ctx); err != nil {
		return err
	}
	return p.readFranchises(ctx)
}

// readSeries asks MusicBrainz once per soundtrack which series it is part of and joins it to each album series.
func (p CollectionPass) readSeries(ctx context.Context) error {
	if p.MusicBrainz == nil {
		return nil
	}
	asked, scanned, joined := 0, 0, 0
	var after int64
	for asked < seriesLookupsPerRun && scanned < seriesScanPerRun && ctx.Err() == nil {
		works, err := p.Franchises.WorksToCheck(ctx, string(MediaType), ProviderReleaseGroup, ProviderMusicBrainzSeries, 0, after, 100)
		if err != nil || len(works) == 0 {
			return err
		}
		var done []int64
		for _, work := range works {
			after = work.WorkID
			scanned++
			if asked >= seriesLookupsPerRun {
				break
			}
			workCtx := ctx
			if p.Libraries != nil {
				workCtx = musicbrainz.ForLibrary(ctx, p.Libraries.OfWork(ctx, work.WorkID))
			}
			group, _, err := p.MusicBrainz.Album(workCtx, musicbrainz.PriorityBackfill, work.ExternalID, work.EditionLabel)
			if err != nil {
				continue
			}
			if !slices.Contains(group.SecondaryTypes, soundtrackType) && !slices.Contains(group.SecondaryTypes, compilationType) {
				done = append(done, work.WorkID)
				continue
			}
			series, cached, err := p.MusicBrainz.ReleaseGroupSeries(workCtx, musicbrainz.PriorityBackfill, work.ExternalID)
			if !cached {
				asked++
			}
			if err != nil {
				continue
			}
			for _, one := range series {
				if one.Type != releaseGroupSeriesType || strings.TrimSpace(one.Name) == "" {
					continue
				}
				if _, err := p.Franchises.Join(ctx, string(MediaType), ProviderMusicBrainzSeries, one.MBID, one.Name, work.WorkID); err != nil {
					return err
				}
				joined++
			}
			done = append(done, work.WorkID)
		}
		if err := p.Franchises.MarkChecked(ctx, ProviderMusicBrainzSeries, done); err != nil {
			return err
		}
	}
	p.Logger.Info("album series read", slog.Int("albums", scanned), slog.Int("asked", asked), slog.Int("joined", joined))
	return nil
}

// readFranchises asks Wikidata for the franchise of every album and keeps a collection for each; nothing changes unless
// Wikidata answered for all of them. MusicBrainz series collections are given the Wikidata items of their series too.
func (p CollectionPass) readFranchises(ctx context.Context) error {
	if p.Wikidata == nil {
		return nil
	}
	ids, err := p.Franchises.WorkProviderIDs(ctx, string(MediaType), ProviderReleaseGroup)
	if err != nil {
		return err
	}
	works := map[string][]int64{}
	var asked []string
	for workID, mbid := range ids {
		if len(works[mbid]) == 0 {
			asked = append(asked, mbid)
		}
		works[mbid] = append(works[mbid], workID)
	}
	slices.Sort(asked)
	found, err := p.Wikidata.AlbumFranchises(ctx, asked)
	if err != nil {
		return jobs.Retryablef("music.collections", string(CollectionPassJobType), err)
	}
	summaries, err := p.Franchises.Summaries(ctx, string(MediaType))
	if err != nil {
		return err
	}
	if err := p.declareSeriesItems(ctx, summaries); err != nil {
		return err
	}
	planned := planAlbumFranchises(found, works)
	kept := map[string]bool{}
	for _, franchise := range planned {
		kept[franchise.QID] = true
		var collection int64
		for _, workID := range franchise.Works {
			joined, err := p.Franchises.Join(ctx, string(MediaType), ProviderWikidata, franchise.QID, franchise.Name, workID)
			if err != nil {
				return err
			}
			collection = max(collection, joined)
		}
		if collection == 0 {
			continue
		}
		members, err := p.Franchises.MembersOf(ctx, collection)
		if err != nil {
			return err
		}
		if gone := withoutWorks(members, franchise.Works); len(gone) > 0 {
			if err := p.Franchises.RemoveFromFranchise(ctx, collection, gone); err != nil {
				return err
			}
		}
		if err := p.Franchises.DeclareKeys(ctx, collection, ProviderWikidata, franchise.Declared); err != nil {
			return err
		}
	}
	dropped := 0
	for _, summary := range summaries {
		if summary.Source != ProviderWikidata || summary.Monitored || slices.ContainsFunc(library.ProviderItems(summary.ExternalIDs, ProviderWikidata), func(qid string) bool {
			return kept[qid]
		}) {
			continue
		}
		if err := p.Franchises.DropFranchise(ctx, summary.ID); err != nil {
			return err
		}
		dropped++
	}
	p.Logger.Info("album franchises found", slog.Int("collections", len(planned)), slog.Int("dropped", dropped))
	return nil
}

// declareSeriesItems gives each MusicBrainz series collection the Wikidata item of its series and the franchises that
// item is part of, which is what links a score series with its films.
func (p CollectionPass) declareSeriesItems(ctx context.Context, summaries []library.FranchiseSummary) error {
	seriesOf := map[int64]string{}
	var mbids []string
	for _, summary := range summaries {
		if items := library.ProviderItems(summary.ExternalIDs, ProviderMusicBrainzSeries); len(items) > 0 {
			seriesOf[summary.ID] = items[0]
			mbids = append(mbids, items[0])
		}
	}
	if len(mbids) == 0 {
		return nil
	}
	slices.Sort(mbids)
	items, err := p.Wikidata.MusicSeriesItems(ctx, mbids)
	if err != nil {
		return jobs.Retryablef("music.collections", string(CollectionPassJobType), err)
	}
	var qids []string
	for _, qid := range items {
		qids = append(qids, qid)
	}
	slices.Sort(qids)
	parts, err := p.Wikidata.ItemFranchises(ctx, qids)
	if err != nil {
		return jobs.Retryablef("music.collections", string(CollectionPassJobType), err)
	}
	ids := make([]int64, 0, len(seriesOf))
	for id := range seriesOf {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		var declared []string
		if qid := items[seriesOf[id]]; qid != "" {
			declared = append([]string{qid}, parts[qid]...)
			slices.Sort(declared)
		}
		if err := p.Franchises.DeclareKeys(ctx, id, ProviderWikidata, declared); err != nil {
			return err
		}
	}
	return nil
}

// albumFranchise is one music collection Wikidata names: its item, name, albums, and every item it declares.
type albumFranchise struct {
	QID, Name string
	Works     []int64
	Declared  []string
}

// planAlbumFranchises joins the franchises Wikidata names for the albums with those they are part of, so each family is
// one collection under its widest item (the saga, not the trilogy a score belongs to) that declares all the others. A
// family reached through a film's soundtrack is kept for one album, as it links the album with the films; one named on
// albums alone needs two.
func planAlbumFranchises(found map[string][]wikidata.AlbumFranchise, works map[string][]int64) []albumFranchise {
	direct := map[string][]int64{}
	labels := map[string]string{}
	parents := map[string][]string{}
	soundtrack := map[string]bool{}
	for mbid, franchises := range found {
		for _, franchise := range franchises {
			for _, workID := range works[mbid] {
				if !slices.Contains(direct[franchise.QID], workID) {
					direct[franchise.QID] = append(direct[franchise.QID], workID)
				}
			}
			labels[franchise.QID], parents[franchise.QID] = franchise.Label, franchise.Parents
			soundtrack[franchise.QID] = soundtrack[franchise.QID] || franchise.Soundtrack
		}
	}
	root := map[string]string{}
	var find func(string) string
	find = func(qid string) string {
		if up, ok := root[qid]; ok && up != qid {
			root[qid] = find(up)
			return root[qid]
		}
		return qid
	}
	for qid, up := range parents {
		for _, parent := range up {
			if _, named := direct[parent]; named && find(qid) != find(parent) {
				root[find(qid)] = find(parent)
			}
		}
	}
	families := map[string][]string{}
	for qid := range direct {
		families[find(qid)] = append(families[find(qid)], qid)
	}
	var out []albumFranchise
	for _, qids := range families {
		slices.Sort(qids)
		entry := albumFranchise{QID: familyTop(qids, parents)}
		entry.Name = labels[entry.QID]
		kept := false
		for _, qid := range qids {
			kept = kept || soundtrack[qid]
			for _, workID := range direct[qid] {
				if !slices.Contains(entry.Works, workID) {
					entry.Works = append(entry.Works, workID)
				}
			}
			for _, declared := range append([]string{qid}, parents[qid]...) {
				if !slices.Contains(entry.Declared, declared) {
					entry.Declared = append(entry.Declared, declared)
				}
			}
		}
		if !kept && len(entry.Works) < 2 || strings.TrimSpace(entry.Name) == "" || entry.Name == entry.QID {
			continue
		}
		slices.Sort(entry.Works)
		slices.Sort(entry.Declared)
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b albumFranchise) int { return strings.Compare(a.QID, b.QID) })
	return out
}

// familyTop is the item of a family that the most others are part of, by the chain of what each is part of; the lowest
// id among equals, so a run names the family the same way each time.
func familyTop(qids []string, parents map[string][]string) string {
	reached := map[string]int{}
	for _, qid := range qids {
		seen := map[string]bool{qid: true}
		stack := []string{qid}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			reached[current]++
			for _, parent := range parents[current] {
				if !seen[parent] && slices.Contains(qids, parent) {
					seen[parent] = true
					stack = append(stack, parent)
				}
			}
		}
	}
	top := qids[0]
	for _, qid := range qids[1:] {
		if reached[qid] > reached[top] {
			top = qid
		}
	}
	return top
}

func withoutWorks(have, keep []int64) []int64 {
	var gone []int64
	for _, id := range have {
		if !slices.Contains(keep, id) {
			gone = append(gone, id)
		}
	}
	return gone
}

// CollectionPassActionKey deduplicates the pass: one at a time.
const CollectionPassActionKey = "music.collections"

// EnqueueCollectionPass submits one pass; a second while one waits is the same work.
func EnqueueCollectionPass(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: CollectionPassJobType, ActionKey: CollectionPassActionKey, Priority: jobs.PriorityLow})
}
