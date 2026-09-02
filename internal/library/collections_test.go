package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// "You have 4 of 6 Ihaol films" is the first thing library-derived discovery
// looks for (ADR-38), so held and stated counts have to come back together.
func TestCollectionCoverageReportsHeldAgainstStated(t *testing.T) {
	opened, queries := newLibrary(t)

	var collection librarydb.Collection
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateCollection(ctx, librarydb.CreateCollectionParams{
			MediaType: "movie", Name: "Ihaol", Kind: "franchise",
			Source: "provider:tmdb", Monitored: 1,
			CompleteAt: sql.NullInt64{Int64: 6, Valid: true}, AddedAt: 1,
		})
		collection = row
		return err
	})

	for index, state := range []string{"COMPLETE", "COMPLETE", "MISSING"} {
		work := createWork(t, opened, "movie", "Ihaol "+string(rune('A'+index)))
		item := createDesired(t, opened, work.ID, "SINGLE", 1)
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			if err := q.SetDesiredItemState(ctx, librarydb.SetDesiredItemStateParams{
				ID: item.ID, State: state, PresentCount: 0,
			}); err != nil {
				return err
			}
			return q.AddCollectionMember(ctx, librarydb.AddCollectionMemberParams{
				CollectionID: collection.ID, WorkID: work.ID,
				Ord: sql.NullInt64{Int64: int64(index), Valid: true},
			})
		})
	}

	coverage, err := queries.CollectionCoverage(t.Context(), collection.ID)
	if err != nil {
		t.Fatalf("CollectionCoverage: %v", err)
	}
	if coverage.Members != 3 {
		t.Errorf("members = %d, want 3", coverage.Members)
	}
	if coverage.Held != 2 {
		t.Errorf("held = %d, want 2", coverage.Held)
	}
	// Unknown must stay distinguishable from zero, or "4 of 0" is displayable.
	if !coverage.CompleteAt.Valid || coverage.CompleteAt.Int64 != 6 {
		t.Errorf("stated total = %+v, want 6", coverage.CompleteAt)
	}
}

// Section 10.5: an entry that waited too long is revalidated, never dispatched.
func TestReadyQueueSkipsExpiredEntries(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	var fresh, stale librarydb.Candidate
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for index, target := range []*librarydb.Candidate{&fresh, &stale} {
			row, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
				IndexerID: int64(index + 1), Guid: "g", Title: "t", Protocol: "usenet",
				DownloadUrl: "u", Source: "search", FirstSeenAt: 1, ExpiresAt: 10_000,
			})
			if err != nil {
				return err
			}
			*target = row
		}
		return nil
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, entry := range []struct {
			candidate int64
			expires   int64
		}{{fresh.ID, 9_000}, {stale.ID, 100}} {
			if err := q.EnqueueReady(ctx, librarydb.EnqueueReadyParams{
				CandidateID: entry.candidate, DesiredItemID: item.ID,
				Priority: 50, QueuedAt: 1, ExpiresAt: entry.expires,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	next, err := queries.NextReady(t.Context(),
		librarydb.NextReadyParams{Now: 1_000, PageSize: 10})
	if err != nil {
		t.Fatalf("NextReady: %v", err)
	}
	if len(next) != 1 || next[0].CandidateID != fresh.ID {
		t.Errorf("the queue offered %d entries, want only the unexpired one", len(next))
	}
}

// ADR-36: the composite priority orders the queue, so the highest goes first
// and ties break on how long they have waited.
func TestReadyQueueOrdersByPriorityThenAge(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")
	item := createDesired(t, opened, work.ID, "SINGLE", 1)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for index, entry := range []struct {
			priority int64
			queued   int64
		}{{10, 1}, {90, 500}, {90, 200}} {
			candidate, err := q.UpsertCandidate(ctx, librarydb.UpsertCandidateParams{
				IndexerID: int64(index + 1), Guid: "g", Title: "t", Protocol: "usenet",
				DownloadUrl: "u", Source: "search", FirstSeenAt: 1, ExpiresAt: 10_000,
			})
			if err != nil {
				return err
			}
			if err := q.EnqueueReady(ctx, librarydb.EnqueueReadyParams{
				CandidateID: candidate.ID, DesiredItemID: item.ID,
				Priority: entry.priority, QueuedAt: entry.queued, ExpiresAt: 9_000,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	next, err := queries.NextReady(t.Context(),
		librarydb.NextReadyParams{Now: 1_000, PageSize: 10})
	if err != nil {
		t.Fatalf("NextReady: %v", err)
	}
	if len(next) != 3 {
		t.Fatalf("%d entries, want 3", len(next))
	}
	if next[0].Priority != 90 || next[0].QueuedAt != 200 {
		t.Errorf("first is priority %d queued at %d, want 90 at 200",
			next[0].Priority, next[0].QueuedAt)
	}
}

// ADR-45: only content-attributable outcomes count. A full disk is not the
// group's fault, and counting it would make reputation measure the environment.
func TestOnlyContentOutcomesCountTowardReputation(t *testing.T) {
	opened, queries := newLibrary(t)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, entry := range []struct {
			outcome string
			cause   string
		}{
			{"clean", "content"},
			{"corrupt", "content"},
			{"unusable", "environment"},
			{"unusable", "user"},
		} {
			if err := q.RecordGroupObservation(ctx, librarydb.RecordGroupObservationParams{
				ReleaseGroup: "GROUP", Outcome: entry.outcome, Cause: entry.cause,
				ObservedAt: 100,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	outcomes, err := queries.CountGroupOutcomes(t.Context(),
		librarydb.CountGroupOutcomesParams{ReleaseGroup: "GROUP", Since: 0})
	if err != nil {
		t.Fatalf("CountGroupOutcomes: %v", err)
	}

	total := int64(0)
	for _, row := range outcomes {
		total += row.Total
	}
	if total != 2 {
		t.Errorf("counted %d observations, want only the 2 attributable to content", total)
	}
}

// Observations are kept individually so reputation is recomputable and decay can
// be reapplied when its half-life changes (ADR-45).
func TestObservationsSurviveTheirSummary(t *testing.T) {
	opened, queries := newLibrary(t)

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.RecordGroupObservation(ctx, librarydb.RecordGroupObservationParams{
			ReleaseGroup: "GROUP", Outcome: "clean", Cause: "content", ObservedAt: 100,
		}); err != nil {
			return err
		}
		return q.SetGroupReputation(ctx, librarydb.SetGroupReputationParams{
			ReleaseGroup: "GROUP", Samples: 1,
			RawSuccess:     sql.NullFloat64{Float64: 1.0, Valid: true},
			BoundedSuccess: sql.NullFloat64{Float64: 0.21, Valid: true},
			UpdatedAt:      200,
		})
	})

	reputation, err := queries.GetGroupReputation(t.Context(), "GROUP")
	if err != nil {
		t.Fatalf("GetGroupReputation: %v", err)
	}
	// The bounded value is what scoring reads, and one lucky import must not
	// produce a high one.
	if reputation.BoundedSuccess.Float64 >= reputation.RawSuccess.Float64 {
		t.Errorf("bounded %v is not below raw %v; a single sample would outrank a long record",
			reputation.BoundedSuccess.Float64, reputation.RawSuccess.Float64)
	}

	observations, err := queries.ListGroupObservations(t.Context(),
		librarydb.ListGroupObservationsParams{ReleaseGroup: "GROUP", ObservedAt: 0})
	if err != nil {
		t.Fatalf("ListGroupObservations: %v", err)
	}
	if len(observations) != 1 {
		t.Errorf("%d observations survived the summary, want 1", len(observations))
	}
}

// One artwork per subject per kind, so a refresh replaces rather than accumulates.
func TestArtworkIsReplacedNotAccumulated(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")

	for _, key := range []string{"old-key", "new-key"} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.UpsertArtwork(ctx, librarydb.UpsertArtworkParams{
				SubjectType: "work", SubjectID: work.ID, Kind: "poster",
				CacheKey: key, FetchedAt: sql.NullInt64{Int64: 1, Valid: true},
			})
		})
	}

	artwork, err := queries.ListArtworkForSubject(t.Context(),
		librarydb.ListArtworkForSubjectParams{SubjectType: "work", SubjectID: work.ID})
	if err != nil {
		t.Fatalf("ListArtworkForSubject: %v", err)
	}
	if len(artwork) != 1 || artwork[0].CacheKey != "new-key" {
		t.Errorf("%d artwork rows, want one holding the newest key", len(artwork))
	}
}
