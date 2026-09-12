package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/clock"
)

// CandidateSearch bridges stage 5's candidate pipeline (internal/media/music.Search)
// to the web interface -- todo 5.2's own "manual search, per-item and
// per-category".
type CandidateSearch struct {
	Works    library.Works
	Search   music.Search
	Queue    *jobs.Queue
	Indexers music.IndexerSource
	Clock    clock.Clock
}

// SearchWork resolves workID to its own album-level wanted item and runs
// the pipeline against it.
func (c CandidateSearch) SearchWork(ctx context.Context, workID int64) (apiserver.SearchItemResult, error) {
	detail, err := c.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	if detail.Wanted == nil {
		return apiserver.SearchItemResult{}, apiserver.ErrWorkNotWanted
	}

	indexers, err := c.Indexers(ctx)
	if err != nil {
		return apiserver.SearchItemResult{}, fmt.Errorf("adapters: candidate search: indexers: %w", err)
	}
	if len(indexers) == 0 {
		return apiserver.SearchItemResult{}, apiserver.ErrNoIndexersConfigured
	}

	result, err := c.Search.SearchItem(ctx, detail.Wanted.ID, indexers, c.Clock.Now())
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	return searchItemResultView(result), nil
}

// ListWorkCandidates answers every decision recorded against workID's own
// wanted album -- empty, not an error, for a work never searched or never
// wanted at all.
func (c CandidateSearch) ListWorkCandidates(ctx context.Context, workID int64) ([]apiserver.CandidateDecision, error) {
	detail, err := c.Works.Detail(ctx, workID)
	if err != nil {
		return nil, err
	}
	if detail.Wanted == nil {
		return nil, nil
	}
	details, err := c.Search.Candidates.ListDecisionsForItem(ctx, detail.Wanted.ID, candidateHistoryLimit)
	if err != nil {
		return nil, err
	}
	views := make([]apiserver.CandidateDecision, len(details))
	for index, detail := range details {
		views[index] = candidateDecisionView(detail)
	}
	return views, nil
}

// candidateHistoryLimit bounds how much of one item's own search history a
// single request answers -- generous for a manual, low-frequency action.
const candidateHistoryLimit = 100

// SearchCategory enqueues music.SearchCategoryJob for categoryID.
func (c CandidateSearch) SearchCategory(ctx context.Context, categoryID int64) (bool, error) {
	result, err := music.EnqueueSearchCategory(ctx, c.Queue, categoryID)
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
}

func searchItemResultView(result music.ItemSearchResult) apiserver.SearchItemResult {
	decisions := make([]apiserver.CandidateDecision, len(result.Decisions))
	for index, detail := range result.Decisions {
		decisions[index] = candidateDecisionView(detail)
	}
	indexerErrors := make([]apiserver.IndexerSearchError, len(result.IndexerErrors))
	for index, indexerError := range result.IndexerErrors {
		indexerErrors[index] = apiserver.IndexerSearchError{IndexerID: indexerError.IndexerID, Error: indexerError.Error}
	}
	return apiserver.SearchItemResult{TierUsed: result.TierUsed, Decisions: decisions, IndexerErrors: indexerErrors}
}

func candidateDecisionView(detail library.DecisionDetail) apiserver.CandidateDecision {
	view := apiserver.CandidateDecision{
		ID: detail.Decision.ID, CandidateID: detail.Decision.CandidateID,
		Outcome: detail.Decision.Outcome, Score: detail.Decision.Score,
		Components:      decodeJSON[model.ScoreComponent](detail.Decision.ComponentsJSON),
		Rejections:      decodeJSON[model.Rejection](detail.Decision.RejectionsJSON),
		Deferrals:       decodeJSON[model.Deferral](detail.Decision.DeferralsJSON),
		MatchConfidence: detail.Decision.MatchConfidence,
		IndexerID:       detail.ReleaseCandidate.IndexerID, GUID: detail.ReleaseCandidate.GUID,
		Title: detail.ReleaseCandidate.Title, SizeBytes: detail.ReleaseCandidate.SizeBytes,
		Seeders: detail.ReleaseCandidate.Seeders, Leechers: detail.ReleaseCandidate.Leechers,
		Protocol: detail.ReleaseCandidate.Protocol, DownloadURL: detail.ReleaseCandidate.DownloadURL,
		Source: detail.ReleaseCandidate.Source, DecidedAt: time.Unix(detail.Decision.DecidedAt, 0),
	}
	if detail.ReleaseCandidate.PublishedAt != nil {
		publishedAt := time.Unix(*detail.ReleaseCandidate.PublishedAt, 0)
		view.PublishedAt = &publishedAt
	}
	return view
}

// decodeJSON parses one candidate_decision JSON column back into its own
// typed slice -- an empty or unparseable value answers nil rather than
// erroring, the same tolerance every other "this is a stored blob, not a
// request body" read in this codebase gives.
func decodeJSON[T any](raw string) []T {
	if raw == "" {
		return nil
	}
	var decoded []T
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil
	}
	return decoded
}
