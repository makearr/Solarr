package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
)

func release(guid string, firstSeen, expires int64) library.NewReleaseCandidate {
	size := int64(500_000_000)
	return library.NewReleaseCandidate{
		IndexerID: 1, GUID: guid, Title: "Nosi Zada-" + guid, SizeBytes: &size, Protocol: "usenet", DownloadURL: "https://x/" + guid,
		Source: "search", FirstSeenAt: firstSeen, ExpiresAt: expires,
	}
}

// A decision is listed with its release, newest first, and carries its library into the statistics.
func TestCandidates_RecordAndList(t *testing.T) {
	candidates := statistics.NewCandidates(openStatistics(t))
	ctx := t.Context()
	first, err := candidates.Upsert(ctx, release("a", 10, 1_000))
	if err != nil {
		t.Fatal(err)
	}
	second, err := candidates.Upsert(ctx, release("b", 20, 1_000))
	if err != nil {
		t.Fatal(err)
	}
	for i, candidate := range []library.ReleaseCandidate{first, second} {
		if _, err := candidates.RecordDecision(ctx, library.NewDecision{
			CandidateID: candidate.ID, DesiredItemID: 7, CategoryID: 3, Outcome: []string{"REJECT", "ACCEPT"}[i],
			Score: float64(i), ComponentsJSON: "[]", DecidedAt: int64(100 + i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	details, err := candidates.ListDecisionsForItem(ctx, 7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 2 || details[0].ReleaseCandidate.GUID != "b" || details[0].Decision.Outcome != "ACCEPT" || *details[0].ReleaseCandidate.SizeBytes != 500_000_000 {
		t.Fatalf("details = %+v, want b's ACCEPT first, with its release", details)
	}
	figures, err := candidates.DecisionsByOutcome(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []statistics.Figure{
		{Metric: "decisions_by_outcome", Dimension: "3|ACCEPT", Value: 1, Count: 1},
		{Metric: "decisions_by_outcome", Dimension: "3|REJECT", Value: 1, Count: 1},
	}
	if !reflect.DeepEqual(figures, want) {
		t.Errorf("decisions by outcome = %+v, want %+v", figures, want)
	}
}

// Aged decisions are counted into the rollup and go; an expired release goes once no decision names it.
func TestCandidates_RetireRollsUpAndDeletes(t *testing.T) {
	candidates := statistics.NewCandidates(openStatistics(t))
	ctx := t.Context()
	old, err := candidates.Upsert(ctx, release("old", 10, 50))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := candidates.Upsert(ctx, release("fresh", 10, 50))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		candidate int64
		at        int64
	}{{old.ID, 86_400}, {old.ID, 86_500}, {fresh.ID, 900_000}} {
		if _, err := candidates.RecordDecision(ctx, library.NewDecision{
			CandidateID: d.candidate, DesiredItemID: 7, Outcome: "REJECT", Score: 2, ComponentsJSON: "[]",
			RejectionsJSON: `[{"Rule":"too-small"}]`, DecidedAt: d.at,
		}); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := candidates.Retire(ctx, time.Unix(100, 0), time.Unix(800_000, 0), 10)
	if err != nil {
		t.Fatal(err)
	}
	if retired != (statistics.Retired{Decisions: 2, Candidates: 1}) {
		t.Errorf("retired %+v, want the two old decisions and the old release, not the one a decision still names", retired)
	}
	rollup, err := candidates.DecisionRollup(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rollup) != 1 || rollup[0].Count != 2 || rollup[0].Period != 1 || rollup[0].TopReason != "too-small" {
		t.Errorf("rollup = %+v, want one day of two REJECTs for too-small", rollup)
	}
}
