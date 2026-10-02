package library_test

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
)

func TestFreshCounts_ReconcileCountsWorksOnOrAfterTheCutoff(t *testing.T) {
	opened, _ := newLibrary(t)
	counts := library.NewFreshCounts(opened, clock.NewFakeAtEpoch())

	createWorkWithDate(t, opened, "Recent Release", "2026-09-05")
	createWorkWithDate(t, opened, "Old Release", "1997-05-21")
	createWorkWithDate(t, opened, "Year Only Release", "2026")
	createWork(t, opened, "music", "No Date At All")

	corrections, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 1 || corrections[0].Metric != "fresh" || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, want one fresh=1 correction on first run", corrections)
	}

	// A second reconcile with nothing changed must not report the same
	// counter as a correction again -- drift-and-repair only reports drift.
	again, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Reconcile made %d corrections, want 0 when nothing changed", len(again))
	}
}

// ADR-55's second window: an old album an RSS feed showed a release for is fresh, a search hit never is.
func TestFreshCounts_AnRSSHitMakesAnOldWorkFreshAndASearchHitDoesNot(t *testing.T) {
	opened, _ := newLibrary(t)
	counts := library.NewFreshCounts(opened, clock.NewFakeAtEpoch())
	viaRSS := createDesired(t, opened, createWorkWithDate(t, opened, "Old via RSS", "1997-05-21").ID, "SET", 10)
	viaSearch := createDesired(t, opened, createWorkWithDate(t, opened, "Old via search", "1997-05-22").ID, "SET", 10)
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, hit := range []struct {
			guid, source string
			item         int64
			seen         int64
		}{{"g1", "rss", viaRSS.ID, 2_000}, {"g2", "search", viaSearch.ID, 2_000}, {"g3", "rss", viaSearch.ID, 10}} {
			candidate, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
				IndexerID: 1, Guid: hit.guid, Title: "t", Protocol: "usenet", DownloadUrl: "u", Source: hit.source,
				FirstSeenAt: hit.seen, ExpiresAt: 1 << 40,
			})
			if err != nil {
				return err
			}
			if _, err := q.RecordDecision(ctx, librarydb.RecordDecisionParams{
				CandidateID: candidate.ID, DesiredItemID: hit.item, Outcome: "REJECT", ComponentsJson: "[]", DecidedAt: hit.seen,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	corrections, err := counts.Reconcile(t.Context(), "2026-09-01", 1_000)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(corrections) != 1 || corrections[0].Now != 1 {
		t.Fatalf("corrections = %+v, want fresh=1: only the work with a recent RSS hit", corrections)
	}
}
