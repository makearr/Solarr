package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

func rollUp(t *testing.T, opened *store.Store, item int64, period int64, outcome string,
	count int64, best float64, reason string,
) {
	t.Helper()
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.RollUpDecisions(ctx, librarydb.RollUpDecisionsParams{
			DesiredItemID: item, Period: period, Outcome: outcome, Count: count,
			BestScore: sql.NullFloat64{Float64: best, Valid: true},
			TopReason: sql.NullString{String: reason, Valid: reason != ""},
		})
	})
}

// Aggregating is what retention is for: a second pass over the same day adds to the
// bucket rather than replacing it, or the count means "the last batch" and not "the day".
func TestDecisionRollup_AccumulatesWithinABucket(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "tender prey")
	item := createDesired(t, opened, work.ID, "SET", 12)

	rollUp(t, opened, item.ID, 20000, "rejected", 3, 0.4, "language")
	rollUp(t, opened, item.ID, 20000, "rejected", 2, 0.9, "quality")

	rows, err := queries.ListDecisionRollup(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListDecisionRollup: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d buckets, want the one day and outcome", len(rows))
	}
	if rows[0].Count != 5 {
		t.Errorf("count = %d, want 5: the second pass adds to the day", rows[0].Count)
	}
	// The best score is the best seen, not the most recent.
	if rows[0].BestScore.Float64 != 0.9 {
		t.Errorf("best_score = %v, want 0.9", rows[0].BestScore.Float64)
	}
}

// A lower score arriving later must not overwrite the best one, or "the closest this ever
// came" becomes "whatever happened last".
func TestDecisionRollup_KeepsTheBestScoreNotTheLatest(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "tender prey")
	item := createDesired(t, opened, work.ID, "SET", 12)

	rollUp(t, opened, item.ID, 20000, "rejected", 1, 0.9, "quality")
	rollUp(t, opened, item.ID, 20000, "rejected", 1, 0.2, "language")

	rows, err := queries.ListDecisionRollup(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListDecisionRollup: %v", err)
	}
	if rows[0].BestScore.Float64 != 0.9 {
		t.Errorf("best_score = %v, want 0.9: a worse later score must not replace it",
			rows[0].BestScore.Float64)
	}
}

// Outcomes are separate buckets: "rejected 40 times" and "accepted once" are different
// facts about the same day.
func TestDecisionRollup_SeparatesOutcomes(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "tender prey")
	item := createDesired(t, opened, work.ID, "SET", 12)

	rollUp(t, opened, item.ID, 20000, "rejected", 40, 0.4, "quality")
	rollUp(t, opened, item.ID, 20000, "accepted", 1, 0.95, "")

	rows, err := queries.ListDecisionRollup(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListDecisionRollup: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d buckets, want one per outcome", len(rows))
	}

	totals, err := queries.CountRollupOutcomes(t.Context(), 0)
	if err != nil {
		t.Fatalf("CountRollupOutcomes: %v", err)
	}
	byOutcome := map[string]int64{}
	for _, row := range totals {
		byOutcome[row.Outcome] = row.Total
	}
	if byOutcome["rejected"] != 40 || byOutcome["accepted"] != 1 {
		t.Errorf("totals = %v, want 40 rejected and 1 accepted", byOutcome)
	}
}

// The rollup is aged detail about a desired item, so deleting the item takes it with it
// rather than leaving rows nothing can explain.
func TestDecisionRollup_CascadesWithItsDesiredItem(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "tender prey")
	item := createDesired(t, opened, work.ID, "SET", 12)
	rollUp(t, opened, item.ID, 20000, "rejected", 3, 0.4, "language")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteWork(ctx, work.ID)
	})

	rows, err := queries.ListDecisionRollup(t.Context(), item.ID)
	if err != nil {
		t.Fatalf("ListDecisionRollup: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("%d rollup rows survived their desired item", len(rows))
	}
}
