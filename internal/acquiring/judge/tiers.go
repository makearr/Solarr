package judge

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/identify/model"
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

// Search widens each indexer through the tiers until it finds a release for the item (AskThroughTiers), then records
// every find at once.
func (p Pipeline) Search(ctx context.Context, itemID int64, conns []Conn, tiers [][]Query, evaluate Evaluate, at time.Time) (Result, error) {
	result, err := p.search(ctx, itemID, conns, tiers, evaluate, at)
	if err != nil {
		return result, err
	}
	return result, p.logSearch(ctx, itemID, result, at)
}

// logSearch records the search for the found rate; whether it found means a release the library accepts.
func (p Pipeline) logSearch(ctx context.Context, itemID int64, result Result, at time.Time) error {
	item, err := p.Desired.Get(ctx, itemID)
	if err != nil {
		return err
	}
	found := slices.ContainsFunc(result.Decisions, func(detail library.DecisionDetail) bool {
		return detail.Decision.Outcome == string(model.OutcomeAccept)
	})
	return p.Candidates.RecordSearch(ctx, item.CategoryID, itemID, found, at)
}

func (p Pipeline) search(ctx context.Context, itemID int64, conns []Conn, tiers [][]Query, evaluate Evaluate, at time.Time) (Result, error) {
	walk, err := AskThroughTiers(ctx, conns, tiers, cmp.Or(p.Deadline, GroupDeadline), func(conn Conn, raws []indexer.RawItem) ([]Find, error) {
		kept, err := p.withoutBlocklisted(ctx, conn.ID, raws, at)
		if err != nil {
			return nil, err
		}
		return evaluate(kept, conn), nil
	})
	result := Result{TierUsed: walk.TierUsed, IndexerErrors: walk.IndexerErrors}
	if err != nil || len(walk.Finds) == 0 {
		return result, err
	}
	result.Decisions, err = p.decide(ctx, itemID, walk.Finds, "search", at)
	return result, err
}

// TierWalk is what asking through the tiers found: every indexer's finds in indexer order, the first tier that found
// anything, and the indexers that failed.
type TierWalk struct {
	Finds         []Find
	TierUsed      *int
	IndexerErrors []IndexerError
}

// AskThroughTiers widens each indexer on its own (Sonarr's per-indexer search): a tier asks the indexers still looking
// that can take one of its queries, and each stops at its first tier with a find or a failure.
func AskThroughTiers(
	ctx context.Context, conns []Conn, tiers [][]Query, deadline time.Duration, evaluate func(Conn, []indexer.RawItem) ([]Find, error),
) (TierWalk, error) {
	var walk TierWalk
	found := make([][]Find, len(conns))
	looking := make([]bool, len(conns))
	for index := range looking {
		looking[index] = true
	}
	for tierIndex, queries := range tiers {
		var asked []int
		for index, conn := range conns {
			if looking[index] && takesAny(conn, queries) {
				asked = append(asked, index)
			}
		}
		if len(asked) == 0 {
			continue
		}
		askedConns := make([]Conn, len(asked))
		for position, index := range asked {
			askedConns[position] = conns[index]
		}
		answers, err := AskEveryIndexer(ctx, askedConns, queries, deadline)
		if err != nil {
			return walk, err
		}
		for position, answer := range answers {
			index, conn := asked[position], askedConns[position]
			if answer.Err != nil {
				walk.IndexerErrors = append(walk.IndexerErrors, IndexerError{
					IndexerID: conn.ID, IndexerName: conn.Name, Error: answer.Err.Error(), TooSlow: answer.TooSlow,
				})
				looking[index] = false
			}
			finds, err := evaluate(conn, answer.Raws)
			if err != nil {
				return walk, err
			}
			if len(finds) > 0 {
				found[index], looking[index] = finds, false
				if walk.TierUsed == nil {
					tier := tierIndex
					walk.TierUsed = &tier
				}
			}
		}
	}
	// Merged in indexer order, so a duplicate keeps the best-priority indexer's copy.
	for _, finds := range found {
		walk.Finds = append(walk.Finds, finds...)
	}
	return walk, nil
}

// takesAny reports whether the indexer can take at least one of a tier's queries.
func takesAny(conn Conn, queries []Query) bool {
	for _, query := range queries {
		if len(query.Needs) == 0 || conn.Takes(query.Function, query.Needs) {
			return true
		}
	}
	return false
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
