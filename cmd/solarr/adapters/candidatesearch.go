package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CandidateSearch bridges stage 5's candidate pipeline
// (internal/media/music.Search and .Dispatch) to the web interface -- todo
// 5.2's own "manual search, per-item and per-category" plus ADR-57's own
// acquire.send.
type CandidateSearch struct {
	Works    library.Works
	Monitor  WorksMonitor
	Search   music.Search
	Dispatch music.Dispatch
	Queue    *jobs.Queue
	Indexers music.IndexerSource
	Clock    clock.Clock
}

// SearchWork resolves workID to its own album-level wanted item and runs
// the pipeline against it.
func (c CandidateSearch) SearchWork(
	ctx context.Context, workID int64, terms *apiserver.SearchTerms,
) (apiserver.SearchItemResult, error) {
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

	var custom *music.SearchTerms
	if terms != nil {
		custom = &music.SearchTerms{Artist: terms.Artist, Album: terms.Album}
	}
	result, err := c.Search.SearchItemWithTerms(ctx, detail.Wanted.ID, indexers, c.Clock.Now(), custom)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	return c.searchItemResultView(ctx, result), nil
}

// BlocklistCandidate blocklists the release one of workID's decisions recorded.
func (c CandidateSearch) BlocklistCandidate(ctx context.Context, workID, decisionID int64) error {
	detail, err := c.Works.Detail(ctx, workID)
	if err != nil {
		return err
	}
	if detail.Wanted == nil {
		return apiserver.ErrCandidateDecisionNotFound
	}
	return translateDispatchError(c.Search.BlocklistRelease(ctx, detail.Wanted.ID, decisionID, c.Clock.Now()))
}

// SearchTrack runs the pipeline against one track's own SINGLE item,
// creating and monitoring it first: asking for a track to be found is
// asking for it.
func (c CandidateSearch) SearchTrack(ctx context.Context, workID, partID int64) (apiserver.SearchItemResult, error) {
	wanted, err := c.Monitor.SetTrackWanted(ctx, workID, partID, true)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}

	indexers, err := c.Indexers(ctx)
	if err != nil {
		return apiserver.SearchItemResult{}, fmt.Errorf("adapters: track search: indexers: %w", err)
	}
	if len(indexers) == 0 {
		return apiserver.SearchItemResult{}, apiserver.ErrNoIndexersConfigured
	}

	result, err := c.Search.SearchItem(ctx, wanted.ID, indexers, c.Clock.Now())
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	return c.searchItemResultView(ctx, result), nil
}

// ListWorkCandidates answers the latest search's decisions for workID's wanted album, best first --
// empty, not an error, for a work never searched or never wanted at all.
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
	return c.decisionViews(ctx, latestRun(details)), nil
}

// latestRun keeps the newest search's decisions: every search re-decides the same releases, and
// listing them all showed one release once per search (2026-09-25). One run shares one time.
func latestRun(details []library.DecisionDetail) []library.DecisionDetail {
	for i, detail := range details {
		if detail.Decision.DecidedAt != details[0].Decision.DecidedAt {
			return details[:i]
		}
	}
	return details
}

// candidateHistoryLimit bounds how much of one item's own search history a
// single request answers -- generous for a manual, low-frequency action.
const candidateHistoryLimit = 100

// SendCandidate resolves workID to its own wanted album item and dispatches
// decisionID to a matching downloader (ADR-57's acquire.send).
func (c CandidateSearch) SendCandidate(ctx context.Context, workID, decisionID int64) (apiserver.Acquisition, error) {
	detail, err := c.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.Acquisition{}, err
	}
	if detail.Wanted == nil {
		return apiserver.Acquisition{}, apiserver.ErrCandidateDecisionNotFound
	}

	acquisition, err := c.Dispatch.Send(ctx, detail.Wanted.ID, decisionID, c.Clock.Now())
	if err != nil {
		return apiserver.Acquisition{}, translateDispatchError(err)
	}
	return acquisitionView(acquisition), nil
}

// translateDispatchError maps music/library's own dispatch error vocabulary
// to apiserver's -- apiserver never imports internal/media/music or
// internal/db/library directly (it defines narrow interfaces), so its own
// sentinels are what a handler actually switches on.
func translateDispatchError(err error) error {
	switch {
	case errors.Is(err, music.ErrDecisionNotFound):
		return apiserver.ErrCandidateDecisionNotFound
	case errors.Is(err, music.ErrDecisionNotAccepted):
		return apiserver.ErrDecisionNotAccepted
	case errors.Is(err, music.ErrNoDownloaderForProtocol):
		return apiserver.ErrNoDownloaderForProtocol
	case errors.Is(err, library.ErrAcquisitionAlreadyInFlight):
		return apiserver.ErrAcquisitionAlreadyInFlight
	case errors.Is(err, download.ErrRefused):
		return fmt.Errorf("%w: %s", apiserver.ErrDownloadClientRefused, err.Error())
	default:
		return err
	}
}

func acquisitionView(acquisition library.Acquisition) apiserver.Acquisition {
	return apiserver.Acquisition{
		ID: acquisition.ID, DownloaderID: acquisition.DownloaderID,
		DownloadClientRef: acquisition.DownloadClientRef, ClientCategory: acquisition.ClientCategory,
		Protocol: acquisition.Protocol, State: acquisition.State,
		CreatedAt: time.Unix(acquisition.CreatedAt, 0), UpdatedAt: time.Unix(acquisition.UpdatedAt, 0),
	}
}

// SearchCategory enqueues music.SearchCategoryJob for categoryID.
func (c CandidateSearch) SearchCategory(ctx context.Context, categoryID int64, workIDs []int64) (bool, error) {
	enqueue := func() (jobs.EnqueueResult, error) { return music.EnqueueSearchCategory(ctx, c.Queue, categoryID) }
	if len(workIDs) > 0 {
		itemIDs := make([]int64, 0, len(workIDs))
		for _, workID := range workIDs {
			detail, err := c.Works.Detail(ctx, workID)
			if err != nil {
				return false, err
			}
			if detail.Wanted != nil {
				itemIDs = append(itemIDs, detail.Wanted.ID)
			}
		}
		enqueue = func() (jobs.EnqueueResult, error) { return music.EnqueueSearchItems(ctx, c.Queue, categoryID, itemIDs) }
	}
	result, err := enqueue()
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
}

// GrabWork is the automatic search of workID's wanted album: search, then send the best release.
func (c CandidateSearch) GrabWork(ctx context.Context, workID int64) (apiserver.GrabResult, error) {
	detail, err := c.Works.Detail(ctx, workID)
	if err != nil {
		return apiserver.GrabResult{}, err
	}
	if detail.Wanted == nil {
		return apiserver.GrabResult{}, apiserver.ErrWorkNotWanted
	}
	indexers, err := c.Indexers(ctx)
	if err != nil {
		return apiserver.GrabResult{}, fmt.Errorf("adapters: grab: indexers: %w", err)
	}
	if len(indexers) == 0 {
		return apiserver.GrabResult{}, apiserver.ErrNoIndexersConfigured
	}
	result, err := music.Grab{Search: c.Search, Dispatch: c.Dispatch}.SearchAndSend(
		ctx, detail.Wanted.ID, indexers, music.GrabUser, c.Clock.Now())
	if err != nil {
		return apiserver.GrabResult{}, translateDispatchError(err)
	}
	view := apiserver.GrabResult{
		Found: len(result.Search.Decisions), AlreadyDownloading: result.AlreadyInFlight, Held: result.Held,
		Refused: result.Refused,
	}
	if result.Sent != nil {
		sent := acquisitionView(*result.Sent)
		view.Sent = &sent
	}
	return view, nil
}

// decisionViews adds what a row shows beyond the decision: its indexer's name, and whether the
// release has been blocklisted since.
func (c CandidateSearch) decisionViews(ctx context.Context, details []library.DecisionDetail) []apiserver.CandidateDecision {
	names := map[int64]string{}
	if c.Search.Settings != nil {
		if indexers, err := c.Search.Settings.Indexers(ctx); err == nil {
			for _, found := range indexers {
				names[found.ID] = found.Name
			}
		}
	}
	now := c.Clock.Now().Unix()
	views := make([]apiserver.CandidateDecision, len(details))
	for index, detail := range details {
		views[index] = candidateDecisionView(detail)
		views[index].IndexerName = names[detail.ReleaseCandidate.IndexerID]
		if c.Search.Blocklist != nil {
			indexerID := detail.ReleaseCandidate.IndexerID
			blocked, err := c.Search.Blocklist.IsBlocked(ctx, &indexerID, detail.ReleaseCandidate.GUID, "", now)
			views[index].Blocklisted = err == nil && blocked
		}
	}
	return views
}

func (c CandidateSearch) searchItemResultView(ctx context.Context, result music.ItemSearchResult) apiserver.SearchItemResult {
	decisions := c.decisionViews(ctx, result.Decisions)
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
		Protocol: detail.ReleaseCandidate.Protocol,
		Source:   detail.ReleaseCandidate.Source, DecidedAt: time.Unix(detail.Decision.DecidedAt, 0),
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
