package library_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

const day = 86_400

// retentionFixture is one wanted album and a way to record releases and decisions about it at chosen times.
type retentionFixture struct {
	opened     *store.Store
	queries    *librarydb.Queries
	candidates library.Candidates
	itemID     int64
	serial     int
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "an album")
	item := createDesired(t, opened, work.ID, "SET", 3)
	return &retentionFixture{opened: opened, queries: queries, candidates: library.NewCandidates(opened), itemID: item.ID}
}

// release records a candidate that expires at expiresAt.
func (f *retentionFixture) release(t *testing.T, expiresAt int64) int64 {
	t.Helper()
	f.serial++
	candidate, err := f.candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: fmt.Sprintf("guid-%d", f.serial), Title: fmt.Sprintf("A Release %d", f.serial),
		Protocol: "usenet", DownloadURL: "https://indexer.example/nzb", Source: "search",
		FirstSeenAt: 0, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	return candidate.ID
}

// decide records one decision about a candidate.
func (f *retentionFixture) decide(t *testing.T, candidateID int64, outcome string, score float64, rejections string, at int64) {
	t.Helper()
	_, err := f.candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: candidateID, DesiredItemID: f.itemID, Outcome: outcome, Score: score,
		ComponentsJSON: "[]", RejectionsJSON: rejections, DeferralsJSON: "[]", DecidedAt: at,
	})
	if err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
}

func (f *retentionFixture) count(t *testing.T, table string) int {
	t.Helper()
	var count int
	if err := f.opened.Read().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

// rollup is one decision_rollup row as the test compares it.
type rollup struct {
	Period    int64
	Outcome   string
	Count     int64
	BestScore float64
	TopReason string
}

func (f *retentionFixture) rollups(t *testing.T) []rollup {
	t.Helper()
	rows, err := f.queries.ListDecisionRollup(t.Context(), f.itemID)
	if err != nil {
		t.Fatalf("ListDecisionRollup: %v", err)
	}
	out := make([]rollup, 0, len(rows))
	for _, row := range rows {
		out = append(out, rollup{row.Period, row.Outcome, row.Count, row.BestScore.Float64, row.TopReason.String})
	}
	return out
}

func TestRetire_AgesOldDecisionsIntoTheDailyRollupAndKeepsRecentOnes(t *testing.T) {
	fixture := newRetentionFixture(t)
	candidate := fixture.release(t, 1000*day)
	quality, language := `[{"Rule":"quality"}]`, `[{"Rule":"language"},{"Rule":"quality"}]`
	fixture.decide(t, candidate, "REJECT", 0.2, quality, 10*day+5)
	fixture.decide(t, candidate, "REJECT", 0.7, quality, 10*day+900)
	fixture.decide(t, candidate, "REJECT", 0.4, language, 10*day+901)
	fixture.decide(t, candidate, "ACCEPT", 0.9, "[]", 10*day+902)
	fixture.decide(t, candidate, "REJECT", 0.1, quality, 11*day)
	fixture.decide(t, candidate, "ACCEPT", 0.8, "[]", 50*day)

	retired, err := fixture.candidates.Retire(t.Context(), time.Unix(60*day, 0), time.Unix(30*day, 0), 10)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if want := (library.Retired{Decisions: 5}); retired != want {
		t.Errorf("Retire = %+v, want %+v", retired, want)
	}
	want := []rollup{
		{Period: 11, Outcome: "REJECT", Count: 1, BestScore: 0.1, TopReason: "quality"},
		{Period: 10, Outcome: "ACCEPT", Count: 1, BestScore: 0.9},
		{Period: 10, Outcome: "REJECT", Count: 3, BestScore: 0.7, TopReason: "quality"},
	}
	if got := fixture.rollups(t); !reflect.DeepEqual(got, want) {
		t.Errorf("rollups = %+v\nwant %+v", got, want)
	}
	if left := fixture.count(t, "candidate_decision"); left != 1 {
		t.Errorf("%d decisions left, want the one recent decision", left)
	}
}

// Running the pass again must not count a decision a second time.
func TestRetire_IsIdempotent(t *testing.T) {
	fixture := newRetentionFixture(t)
	candidate := fixture.release(t, 1000*day)
	fixture.decide(t, candidate, "REJECT", 0.2, `[{"Rule":"quality"}]`, 10*day)

	for range 2 {
		if _, err := fixture.candidates.Retire(t.Context(), time.Unix(60*day, 0), time.Unix(30*day, 0), 10); err != nil {
			t.Fatalf("Retire: %v", err)
		}
	}
	want := []rollup{{Period: 10, Outcome: "REJECT", Count: 1, BestScore: 0.2, TopReason: "quality"}}
	if got := fixture.rollups(t); !reflect.DeepEqual(got, want) {
		t.Errorf("rollups after two passes = %+v, want %+v", got, want)
	}
}

func TestRetire_DeletesAnExpiredCandidateOnlyWhenNothingRefersToIt(t *testing.T) {
	fixture := newRetentionFixture(t)
	expiredAlone := fixture.release(t, 40*day)
	expiredWithRecentDecision := fixture.release(t, 40*day)
	expiredButHeld := fixture.release(t, 40*day)
	expiredWithOldDecision := fixture.release(t, 40*day)
	stillValid := fixture.release(t, 90*day)
	fixture.decide(t, expiredWithRecentDecision, "ACCEPT", 0.9, "[]", 50*day)
	fixture.decide(t, expiredWithOldDecision, "REJECT", 0.1, "[]", 10*day)
	write(t, fixture.opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.EnqueueReady(ctx, librarydb.EnqueueReadyParams{
			CandidateID: expiredButHeld, DesiredItemID: fixture.itemID, Priority: 1, QueuedAt: 41 * day, ExpiresAt: 99 * day, Origin: "automatic",
		})
	})

	retired, err := fixture.candidates.Retire(t.Context(), time.Unix(60*day, 0), time.Unix(30*day, 0), 10)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if want := (library.Retired{Decisions: 1, Candidates: 2}); retired != want {
		t.Errorf("Retire = %+v, want %+v", retired, want)
	}
	kept := map[int64]bool{}
	for _, id := range []int64{expiredAlone, expiredWithRecentDecision, expiredButHeld, expiredWithOldDecision, stillValid} {
		_, err := fixture.candidates.Get(t.Context(), id)
		kept[id] = err == nil
	}
	want := map[int64]bool{
		expiredAlone: false, expiredWithOldDecision: false,
		expiredWithRecentDecision: true, expiredButHeld: true, stillValid: true,
	}
	if !reflect.DeepEqual(kept, want) {
		t.Errorf("candidates kept = %v, want %v", kept, want)
	}
}

// A backlog larger than one page is retired over several pages, and a page cap leaves the rest for the next run.
func TestRetire_WorksThroughABacklogInPages(t *testing.T) {
	fixture := newRetentionFixture(t)
	candidate := fixture.release(t, 1000*day)
	for index := range 450 {
		fixture.decide(t, candidate, "REJECT", 0.1, "[]", int64(10*day+index))
	}

	first, err := fixture.candidates.Retire(t.Context(), time.Unix(60*day, 0), time.Unix(30*day, 0), 2)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	second, err := fixture.candidates.Retire(t.Context(), time.Unix(60*day, 0), time.Unix(30*day, 0), 2)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if got := [2]int64{first.Decisions, second.Decisions}; got != [2]int64{400, 50} {
		t.Errorf("decisions retired by (first, second) run = %v, want [400 50]", got)
	}
	want := []rollup{{Period: 10, Outcome: "REJECT", Count: 450, BestScore: 0.1}}
	if got := fixture.rollups(t); !reflect.DeepEqual(got, want) {
		t.Errorf("rollups = %+v, want %+v", got, want)
	}
}
