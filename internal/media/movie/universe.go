package movie

import (
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// ProviderWikidata names a universe by the Wikidata item of its franchise.
const ProviderWikidata = "wikidata"

// UniversePassJobType is the registered type of the pass that groups films into the franchises and universes Wikidata
// names, beyond the collections TMDB has.
const UniversePassJobType = jobs.Type("movie.universes")

// UniversePassDefinition declares the pass: background metadata work, retried when Wikidata is busy.
var UniversePassDefinition = jobs.Definition{
	Name: "Grouping films into franchises and universes",
	Type: UniversePassJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// UniverseSource is Wikidata as the pass asks it, many films at a time.
type UniverseSource interface {
	FilmFranchises(ctx context.Context, tmdbIDs []int64) (map[int64][]wikidata.Franchise, error)
}

// UniversePass keeps a collection for each franchise or universe Wikidata puts two or more films of the library in, when
// that says more than a TMDB collection already does: Ihaol with Sfozutvaew and Ihaol: Romulus, the Marvel Cinematic
// Universe with its phases and the films of its heroes. A film stays in its TMDB collection as well.
type UniversePass struct {
	Source     UniverseSource
	Franchises library.Franchises
	Logger     *slog.Logger
}

// Run implements jobs.Handler. Nothing changes unless Wikidata answered for every film.
func (p *UniversePass) Run(ctx context.Context, _ jobs.Job) error {
	ids, err := p.Franchises.WorkProviderIDs(ctx, MediaType, ProviderTMDB)
	if err != nil {
		return err
	}
	byTMDB := map[int64]int64{}
	var asked []int64
	for workID, external := range ids {
		if tmdbID, err := strconv.ParseInt(external, 10, 64); err == nil && tmdbID > 0 {
			byTMDB[tmdbID] = workID
			asked = append(asked, tmdbID)
		}
	}
	slices.Sort(asked)
	found, err := p.Source.FilmFranchises(ctx, asked)
	if err != nil {
		return jobs.Retryablef("movie.universes", string(UniversePassJobType), err)
	}
	direct := map[string][]int64{}
	labels := map[string]string{}
	parents := map[string][]string{}
	for tmdbID, franchises := range found {
		for _, franchise := range franchises {
			direct[franchise.QID] = append(direct[franchise.QID], byTMDB[tmdbID])
			labels[franchise.QID] = franchise.Label
			parents[franchise.QID] = franchise.Parents
		}
	}
	summaries, err := p.Franchises.Summaries(ctx, MediaType)
	if err != nil {
		return err
	}
	memberships, err := p.Franchises.FranchiseMemberships(ctx, MediaType)
	if err != nil {
		return err
	}
	var held [][]int64
	for _, summary := range summaries {
		if summary.Source == ProviderWikidata {
			continue
		}
		var members []int64
		for _, membership := range memberships {
			if membership.CollectionID == summary.ID {
				members = append(members, membership.WorkID)
			}
		}
		held = append(held, members)
	}
	universes := planUniverses(direct, labels, parents, held)
	kept := map[string]bool{}
	for _, universe := range universes {
		kept[universe.QID] = true
		var collection int64
		for _, workID := range universe.Works {
			joined, err := p.Franchises.Join(ctx, MediaType, ProviderWikidata, universe.QID, universe.Name, workID)
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
		if gone := without(members, universe.Works); len(gone) > 0 {
			if err := p.Franchises.RemoveFromFranchise(ctx, collection, gone); err != nil {
				return err
			}
		}
	}
	dropped := 0
	for _, summary := range summaries {
		if summary.Source != ProviderWikidata || summary.Monitored || slices.ContainsFunc(summary.ExternalIDs, func(id string) bool {
			qid, ours := strings.CutPrefix(id, ProviderWikidata+":")
			return ours && kept[qid]
		}) {
			continue
		}
		if err := p.Franchises.DropFranchise(ctx, summary.ID); err != nil {
			return err
		}
		dropped++
	}
	p.Logger.Info("movie universes found", slog.Int("universes", len(universes)), slog.Int("dropped", dropped))
	return nil
}

func without(have, keep []int64) []int64 {
	var gone []int64
	for _, id := range have {
		if !slices.Contains(keep, id) {
			gone = append(gone, id)
		}
	}
	return gone
}

// Universe is one franchise or universe of the library: the Wikidata item that names it, its name and its films.
type Universe struct {
	QID, Name string
	Works     []int64
}

// planUniverses joins the franchises that are part of one another the library also has a film of into one, and keeps
// those of two or more films that no other holds in full and no TMDB collection already is.
func planUniverses(direct map[string][]int64, labels map[string]string, parents map[string][]string, held [][]int64) []Universe {
	root := map[string]string{}
	for qid := range direct {
		root[qid] = qid
	}
	var find func(string) string
	find = func(qid string) string {
		if root[qid] != qid {
			root[qid] = find(root[qid])
		}
		return root[qid]
	}
	for qid, up := range parents {
		for _, parent := range up {
			if _, named := direct[parent]; named {
				root[find(qid)] = find(parent)
			}
		}
	}
	components := map[string][]string{}
	for qid := range direct {
		components[find(qid)] = append(components[find(qid)], qid)
	}
	var all []Universe
	for top, qids := range components {
		works := map[int64]bool{}
		for _, qid := range qids {
			for _, workID := range direct[qid] {
				works[workID] = true
			}
		}
		if len(works) < 2 {
			continue
		}
		// The top of the parts is the whole: "Marvel Cinematic Universe", not "Phase One".
		universe := Universe{QID: top, Name: labels[top]}
		for workID := range works {
			universe.Works = append(universe.Works, workID)
		}
		slices.Sort(universe.Works)
		all = append(all, universe)
	}
	slices.SortFunc(all, func(a, b Universe) int {
		if len(a.Works) != len(b.Works) {
			return len(b.Works) - len(a.Works)
		}
		return compareStrings(a.QID, b.QID)
	})
	var kept []Universe
	for index, universe := range all {
		if holds(held, universe.Works) {
			continue
		}
		if slices.ContainsFunc(all[:index], func(larger Universe) bool { return containsAll(larger.Works, universe.Works) }) {
			continue
		}
		kept = append(kept, universe)
	}
	return kept
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func containsAll(have, want []int64) bool {
	for _, id := range want {
		if !slices.Contains(have, id) {
			return false
		}
	}
	return true
}

// holds says one of the collections already has every one of the works.
func holds(collections [][]int64, works []int64) bool {
	return slices.ContainsFunc(collections, func(members []int64) bool { return containsAll(members, works) })
}

// UniversePassActionKey deduplicates the pass: one at a time.
const UniversePassActionKey = "movie.universes"

// EnqueueUniversePass submits one pass; a second while one waits is the same work.
func EnqueueUniversePass(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: UniversePassJobType, ActionKey: UniversePassActionKey, Priority: jobs.PriorityLow})
}
