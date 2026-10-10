package judge

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
)

// GroupDeadline bounds one tier's fan-out to every indexer (ADR-37's default), whatever each indexer's own timeout.
const GroupDeadline = 20 * time.Second

// IndexerError is one indexer's failure within a search; partial results are normal, not an error (ADR-37).
type IndexerError struct {
	IndexerID   int64
	IndexerName string
	Error       string
	// TooSlow means the indexer had not answered when the group deadline passed.
	TooSlow bool
}

// Result is one wanted item's search: which tier found something, every decision recorded, best first, and the
// indexers that failed along the way.
type Result struct {
	// TierUsed is nil when no tier found anything worth recording, which is not a failure.
	TierUsed      *int
	Decisions     []library.DecisionDetail
	IndexerErrors []IndexerError
}

// Evaluate is a media type's part of a search: which of one indexer's releases are for the wanted item, and how
// each is judged.
type Evaluate func(raws []indexer.RawItem, conn Conn) []Find

// Pipeline is the search every media type shares: ask, drop what is blocklisted, let the type evaluate, record.
type Pipeline struct {
	// Candidates record what was found and decided, in statistics.db: nothing that decides reads them back.
	Candidates statistics.Candidates
	// Desired names the item's library, kept beside each decision.
	Desired library.Desired
	// Sightings, when set, remember when RSS showed a release for an item: what "fresh" reads (ADR-55).
	Sightings *library.Sightings
	// Blocklist, when set, keeps a blocklisted release out before it is evaluated (section 10.11).
	Blocklist *library.BlocklistStore
	// Deadline bounds each tier's fan-out; zero is GroupDeadline.
	Deadline time.Duration
}

// Search widens through tiers until one finds a release for the item: every indexer is asked a tier's queries at
// once, and the first tier with a find records all of them and ends the search.
func (p Pipeline) Search(ctx context.Context, itemID int64, conns []Conn, tiers [][]Query, evaluate Evaluate, at time.Time) (Result, error) {
	var result Result
	for tierIndex, queries := range tiers {
		answers, err := AskEveryIndexer(ctx, conns, queries, cmp.Or(p.Deadline, GroupDeadline))
		if err != nil {
			return result, err
		}
		// Merged in indexer order, so a duplicate keeps the best-priority indexer's copy.
		var finds []Find
		for index, answer := range answers {
			conn := conns[index]
			if answer.Err != nil {
				result.IndexerErrors = append(result.IndexerErrors, IndexerError{
					IndexerID: conn.ID, IndexerName: conn.Name, Error: answer.Err.Error(), TooSlow: answer.TooSlow,
				})
			}
			raws, err := p.withoutBlocklisted(ctx, conn.ID, answer.Raws, at)
			if err != nil {
				return result, err
			}
			finds = append(finds, evaluate(raws, conn)...)
		}
		if len(finds) == 0 {
			continue
		}
		tier := tierIndex
		result.TierUsed = &tier
		result.Decisions, err = p.decide(ctx, itemID, finds, "search", at)
		return result, err
	}
	return result, nil
}

// Feed judges releases an RSS sync read from one indexer, exactly as a search's would be; nothing is asked.
func (p Pipeline) Feed(ctx context.Context, itemID int64, conn Conn, raws []indexer.RawItem, evaluate Evaluate, at time.Time) (Result, error) {
	raws, err := p.withoutBlocklisted(ctx, conn.ID, raws, at)
	if err != nil {
		return Result{}, err
	}
	finds := evaluate(raws, conn)
	if len(finds) == 0 {
		return Result{}, nil
	}
	decisions, err := p.decide(ctx, itemID, finds, "rss", at)
	return Result{Decisions: decisions}, err
}

func (p Pipeline) withoutBlocklisted(ctx context.Context, indexerID int64, raws []indexer.RawItem, at time.Time) ([]indexer.RawItem, error) {
	if p.Blocklist == nil {
		return raws, nil
	}
	kept := raws[:0:0]
	for _, raw := range raws {
		blocked, err := p.Blocklist.IsBlocked(ctx, &indexerID, raw.GUID, "", at.Unix())
		if err != nil {
			return nil, err
		}
		if !blocked {
			kept = append(kept, raw)
		}
	}
	return kept, nil
}

// decide keeps one row per release, ranks them best first and records each decision against itemID.
func (p Pipeline) decide(ctx context.Context, itemID int64, finds []Find, source string, at time.Time) ([]library.DecisionDetail, error) {
	if len(finds) == 0 {
		return nil, nil
	}
	item, err := p.Desired.Get(ctx, itemID)
	if err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	finds = WithoutCrossIndexerDuplicates(finds)
	SortBestFirst(finds)
	decisions := make([]library.DecisionDetail, 0, len(finds))
	for _, find := range finds {
		detail, err := Persist(ctx, p.Candidates, p.Sightings, item.ID, item.CategoryID, find, source, at)
		if err != nil {
			return decisions, fmt.Errorf("persist: %w", err)
		}
		decisions = append(decisions, detail)
	}
	return decisions, nil
}
