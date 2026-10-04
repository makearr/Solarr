package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// retentionPage is Invariant 2's bulk chunk: how many decisions or candidates one transaction retires.
const retentionPage = 200

// secondsPerDay sizes decision_rollup's day bucket.
const secondsPerDay = 86_400

// Retired counts what one retention pass removed.
type Retired struct {
	Decisions  int64
	Candidates int64
}

// rollupKey is one decision_rollup row: an item, a day and an outcome.
type rollupKey struct {
	itemID  int64
	period  int64
	outcome string
}

// rollupSum is what a page of decisions adds to one rollup row.
type rollupSum struct {
	count   int64
	best    float64
	reasons map[string]int
}

// Retire ages decisions older than decisionsBefore into decision_rollup and deletes them, then deletes the
// candidates that expired by now and that no decision or held release refers to any more. It works in pages of
// retentionPage, at most maxPages of each, so a first run over a large backlog is spread over several runs.
func (c Candidates) Retire(ctx context.Context, now, decisionsBefore time.Time, maxPages int) (Retired, error) {
	var retired Retired
	for range maxPages {
		removed, err := c.retireDecisionPage(ctx, decisionsBefore.Unix())
		if err != nil {
			return retired, err
		}
		retired.Decisions += removed
		if removed == 0 {
			break
		}
	}
	for range maxPages {
		var removed int64
		err := c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			removed, err = librarydb.New(tx).DeleteUnreferencedExpiredCandidates(ctx,
				librarydb.DeleteUnreferencedExpiredCandidatesParams{Now: now.Unix(), PageSize: retentionPage})
			return err
		})
		if err != nil {
			return retired, fmt.Errorf("library: retire candidates: %w", err)
		}
		retired.Candidates += removed
		if removed < retentionPage {
			break
		}
	}
	return retired, nil
}

// retireDecisionPage rolls the oldest page's aged decisions up and deletes them, in one transaction so a decision is
// never counted twice or lost between the two.
func (c Candidates) retireDecisionPage(ctx context.Context, decidedBefore int64) (int64, error) {
	rows, err := librarydb.New(c.store.Read()).ListDecisionsToRetire(ctx,
		librarydb.ListDecisionsToRetireParams{PageSize: retentionPage, DecidedBefore: decidedBefore})
	if err != nil {
		return 0, fmt.Errorf("library: list decisions to retire: %w", err)
	}
	if len(rows) == 0 {
		return 0, nil
	}

	sums := make(map[rollupKey]*rollupSum)
	var order []rollupKey
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
		key := rollupKey{itemID: row.DesiredItemID, period: row.DecidedAt / secondsPerDay, outcome: row.Outcome}
		sum, seen := sums[key]
		if !seen {
			sum = &rollupSum{best: row.Score, reasons: make(map[string]int)}
			sums[key] = sum
			order = append(order, key)
		}
		sum.count++
		sum.best = max(sum.best, row.Score)
		if reason := firstRejectionRule(row.RejectionsJson); reason != "" {
			sum.reasons[reason]++
		}
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return 0, fmt.Errorf("library: encode decision ids: %w", err)
	}

	var removed int64
	err = c.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, key := range order {
			sum := sums[key]
			reason := mostFrequent(sum.reasons)
			if err := queries.RollUpDecisions(ctx, librarydb.RollUpDecisionsParams{
				DesiredItemID: key.itemID, Period: key.period, Outcome: key.outcome, Count: sum.count,
				BestScore: sql.NullFloat64{Float64: sum.best, Valid: true},
				TopReason: sql.NullString{String: reason, Valid: reason != ""},
			}); err != nil {
				return fmt.Errorf("roll up: %w", err)
			}
		}
		var err error
		removed, err = queries.DeleteDecisionsByID(ctx, string(encoded))
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: retire decisions: %w", err)
	}
	return removed, nil
}

// firstRejectionRule is the rule of a decision's first rejection, the reason it is remembered by; empty when it has none.
func firstRejectionRule(raw sql.NullString) string {
	if !raw.Valid || raw.String == "" {
		return ""
	}
	var rejections []struct{ Rule string }
	if err := json.Unmarshal([]byte(raw.String), &rejections); err != nil || len(rejections) == 0 {
		return ""
	}
	return rejections[0].Rule
}

// mostFrequent is the reason counted most often, the alphabetically first among equals so a rerun answers the same.
func mostFrequent(reasons map[string]int) string {
	names := make([]string, 0, len(reasons))
	for name := range reasons {
		names = append(names, name)
	}
	slices.Sort(names)
	best := ""
	for _, name := range names {
		if best == "" || reasons[name] > reasons[best] {
			best = name
		}
	}
	return best
}
