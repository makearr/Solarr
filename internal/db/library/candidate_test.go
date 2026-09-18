package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func TestCandidates_UpsertRefreshesClaimedFactsOnARepeatedFind(t *testing.T) {
	opened, _ := newLibrary(t)
	candidates := library.NewCandidates(opened)

	seeders := int64(10)
	first, err := candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "abc123", Title: "Artist-Album-2020-FLAC-GROUP",
		Protocol: "usenet", DownloadURL: "https://indexer/nzb/abc123", Source: "search",
		Seeders: &seeders, FirstSeenAt: 1, ExpiresAt: 100,
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	moreSeeders := int64(42)
	second, err := candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "abc123", Title: "Artist-Album-2020-FLAC-GROUP",
		Protocol: "usenet", DownloadURL: "https://indexer/nzb/abc123", Source: "search",
		Seeders: &moreSeeders, FirstSeenAt: 1, ExpiresAt: 200,
	})
	if err != nil {
		t.Fatalf("Upsert (repeat): %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("second.ID = %d, want the same row as the first find (%d) -- (indexer_id, guid) must upsert, not duplicate",
			second.ID, first.ID)
	}
	if second.Seeders == nil || *second.Seeders != 42 {
		t.Errorf("Seeders = %v, want the refreshed value 42", second.Seeders)
	}
	if second.ExpiresAt != 200 {
		t.Errorf("ExpiresAt = %d, want the refreshed value 200", second.ExpiresAt)
	}
}

func TestCandidates_ListDecisionsForItem_JoinsTheCandidateAndOrdersNewestFirst(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "Discovery")
	item := createDesired(t, opened, work.ID, "SET", 12)
	candidates := library.NewCandidates(opened)

	older, err := candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "guid-older", Title: "Older Release",
		Protocol: "usenet", DownloadURL: "https://indexer/nzb/older", Source: "search",
		FirstSeenAt: 1, ExpiresAt: 100,
	})
	if err != nil {
		t.Fatalf("Upsert (older): %v", err)
	}
	newer, err := candidates.Upsert(t.Context(), library.NewReleaseCandidate{
		IndexerID: 1, GUID: "guid-newer", Title: "Newer Release",
		Protocol: "usenet", DownloadURL: "https://indexer/nzb/newer", Source: "search",
		FirstSeenAt: 1, ExpiresAt: 100,
	})
	if err != nil {
		t.Fatalf("Upsert (newer): %v", err)
	}

	if _, err := candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: older.ID, DesiredItemID: item.ID, Outcome: "REJECT",
		Score: 0, ComponentsJSON: "[]", RejectionsJSON: `[{"rule":"format"}]`, DecidedAt: 10,
	}); err != nil {
		t.Fatalf("RecordDecision (older): %v", err)
	}
	if _, err := candidates.RecordDecision(t.Context(), library.NewDecision{
		CandidateID: newer.ID, DesiredItemID: item.ID, Outcome: "ACCEPT",
		Score: 12.5, ComponentsJSON: `[{"rule":"format","delta":12.5}]`, DecidedAt: 20,
	}); err != nil {
		t.Fatalf("RecordDecision (newer): %v", err)
	}

	details, err := candidates.ListDecisionsForItem(t.Context(), item.ID, 10)
	if err != nil {
		t.Fatalf("ListDecisionsForItem: %v", err)
	}
	if len(details) != 2 {
		t.Fatalf("%d decisions, want 2", len(details))
	}
	if details[0].ReleaseCandidate.Title != "Newer Release" || details[0].Decision.Outcome != "ACCEPT" {
		t.Errorf("details[0] = %+v, want the newer, accepted decision first", details[0])
	}
	if details[1].ReleaseCandidate.Title != "Older Release" || details[1].Decision.Outcome != "REJECT" {
		t.Errorf("details[1] = %+v, want the older, rejected decision second", details[1])
	}
	if details[0].Decision.Score != 12.5 {
		t.Errorf("Score = %v, want 12.5", details[0].Decision.Score)
	}
}
