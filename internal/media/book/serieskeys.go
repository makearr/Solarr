package book

import (
	"context"
	"log/slog"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

// seriesKeysBatch bounds the Open Library ids one Wikidata query names.
const seriesKeysBatch = 200

// SeriesKeysJobType is the registered type of the pass that tells each book series collection its Wikidata item.
const SeriesKeysJobType = jobs.Type("book.series_keys")

// SeriesKeysDefinition declares the pass: background metadata work, retried when Wikidata is busy.
var SeriesKeysDefinition = jobs.Definition{
	Name: "Finding the franchises book series belong to",
	Type: SeriesKeysJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// SeriesKeySource is Wikidata as the pass asks it: the series of books, and the franchises an item is part of.
type SeriesKeySource interface {
	SeriesSource
	ItemFranchises(ctx context.Context, qids []string) (map[string][]string, error)
}

// SeriesKeysPass declares, on each book series collection (kept by name), the Wikidata item of its series and the
// franchises that series is part of, so the series is linked with the films and shows of the same franchise.
type SeriesKeysPass struct {
	Formats    []Format
	Source     SeriesKeySource
	Franchises library.Franchises
	Logger     *slog.Logger
}

// Run implements jobs.Handler; nothing is declared for a format unless Wikidata answered for all its books.
func (p SeriesKeysPass) Run(ctx context.Context, _ jobs.Job) error {
	declared := 0
	for _, format := range p.Formats {
		count, err := p.declare(ctx, format)
		if err != nil {
			return err
		}
		declared += count
	}
	p.Logger.Info("book series keys declared", slog.Int("series", declared))
	return nil
}

func (p SeriesKeysPass) declare(ctx context.Context, format Format) (int, error) {
	summaries, err := p.Franchises.Summaries(ctx, format.MediaType)
	if err != nil {
		return 0, err
	}
	memberships, err := p.Franchises.FranchiseMemberships(ctx, format.MediaType)
	if err != nil {
		return 0, err
	}
	ids, err := p.Franchises.WorkProviderIDs(ctx, format.MediaType, format.Provider(ProviderOpenLibrary))
	if err != nil {
		return 0, err
	}
	held := map[int64][]string{}
	var asked []string
	for _, membership := range memberships {
		if id := ids[membership.WorkID]; id != "" {
			held[membership.CollectionID] = append(held[membership.CollectionID], id)
			asked = append(asked, id)
		}
	}
	slices.Sort(asked)
	asked = slices.Compact(asked)
	var books []wikidata.SeriesBook
	for start := 0; start < len(asked); start += seriesKeysBatch {
		batch, err := p.Source.BookSeries(ctx, asked[start:min(start+seriesKeysBatch, len(asked))])
		if err != nil {
			return 0, jobs.Retryablef("book.series_keys", string(SeriesKeysJobType), err)
		}
		books = append(books, batch...)
	}
	chosen := map[int64]string{}
	var qids []string
	for _, summary := range summaries {
		if summary.Source != ProviderSeries {
			continue
		}
		if qid := seriesItem(books, held[summary.ID], summary.Name); qid != "" {
			chosen[summary.ID] = qid
			qids = append(qids, qid)
		}
	}
	slices.Sort(qids)
	franchises, err := p.Source.ItemFranchises(ctx, slices.Compact(qids))
	if err != nil {
		return 0, jobs.Retryablef("book.series_keys", string(SeriesKeysJobType), err)
	}
	declared := 0
	for _, summary := range summaries {
		if summary.Source != ProviderSeries {
			continue
		}
		var keys []string
		if qid := chosen[summary.ID]; qid != "" {
			keys = append([]string{qid}, franchises[qid]...)
			slices.Sort(keys)
			declared++
		}
		if err := p.Franchises.DeclareKeys(ctx, summary.ID, ProviderWikidata, keys); err != nil {
			return declared, err
		}
	}
	return declared, nil
}

// seriesItem is the Wikidata series a collection is: the one named like it, else the one more than half its books are in;
// "" when neither, as a book of a franchise does not make its collection that franchise's.
func seriesItem(books []wikidata.SeriesBook, held []string, name string) string {
	if len(held) == 0 {
		return ""
	}
	qid, inside, named := chooseSeries(books, held, name)
	if named || inside*2 > len(held) {
		return qid
	}
	return ""
}

// SeriesKeysActionKey deduplicates the pass: one at a time.
const SeriesKeysActionKey = "book.series_keys"

// EnqueueSeriesKeys submits one pass; a second while one waits is the same work.
func EnqueueSeriesKeys(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: SeriesKeysJobType, ActionKey: SeriesKeysActionKey, Priority: jobs.PriorityLow})
}
