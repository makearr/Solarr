package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// CandidateSearch bridges the search a person asks for to the web interface: todo 5.2's manual search, per item and
// per library, plus ADR-57's acquire.send. The type's own search runs through its media.Searching.
type CandidateSearch struct {
	Works   library.Works
	Desired library.Desired
	// Searching answers a media type's search; nil or false leaves a type unsearchable.
	Searching func(mediaType string) (media.Searching, bool)
	// Search carries the stores decision rows and blocklisting read; any type's will do.
	Search   music.Search
	Dispatch music.Dispatch
	Queue    *jobs.Queue
	Clock    clock.Clock
}

// target resolves a scope to the wanted item it is searched through and the part ref it narrows to.
func (c CandidateSearch) target(ctx context.Context, workID int64, scope apiserver.SearchScope) (int64, string, error) {
	itemID, part, err := c.Works.WantedItemFor(ctx, workID, library.SearchScope{Group: scope.Group, PartID: scope.Part})
	switch {
	case errors.Is(err, library.ErrNoWantedItem):
		return 0, "", apiserver.ErrWorkNotWanted
	case errors.Is(err, library.ErrPartNotFound):
		return 0, "", apiserver.ErrPartNotFound
	}
	return itemID, part, err
}

// searching resolves the scope and the work's type's search.
func (c CandidateSearch) searching(ctx context.Context, workID int64, scope apiserver.SearchScope) (media.Searching, int64, string, error) {
	mediaType, err := c.Works.MediaTypeOf(ctx, workID)
	if err != nil {
		return nil, 0, "", err
	}
	searching, ok := media.Searching(nil), false
	if c.Searching != nil {
		searching, ok = c.Searching(mediaType)
	}
	if !ok {
		return nil, 0, "", apiserver.ErrWorkNotWanted
	}
	itemID, part, err := c.target(ctx, workID, scope)
	return searching, itemID, part, err
}

// SearchWork runs the work's type's interactive search against the scope's wanted item.
func (c CandidateSearch) SearchWork(
	ctx context.Context, workID int64, scope apiserver.SearchScope, terms map[string]string,
) (apiserver.SearchItemResult, error) {
	searching, itemID, part, err := c.searching(ctx, workID, scope)
	if err != nil {
		return apiserver.SearchItemResult{}, err
	}
	result, err := searching.SearchWanted(ctx, media.WantedSearch{ItemID: itemID, Part: part, Terms: terms})
	if err != nil {
		return apiserver.SearchItemResult{}, searchError(err)
	}
	return c.searchItemResultView(ctx, result), nil
}

// GrabWork is the automatic search of the scope: search, then send the best release. A whole work wanted group by
// group (a series) is Sonarr's "Search monitored": each monitored group that is not complete, in turn.
func (c CandidateSearch) GrabWork(ctx context.Context, workID int64, scope apiserver.SearchScope) (apiserver.GrabResult, error) {
	if scope.Group == nil && scope.Part == nil {
		whole, refs, err := c.Desired.GroupsToSearch(ctx, workID)
		if err != nil {
			return apiserver.GrabResult{}, err
		}
		if !whole {
			return c.grabGroups(ctx, workID, refs)
		}
	}
	return c.grabOne(ctx, workID, scope)
}

// grabGroups searches each group in turn and adds up what they came to.
func (c CandidateSearch) grabGroups(ctx context.Context, workID int64, refs []string) (apiserver.GrabResult, error) {
	if len(refs) == 0 {
		return apiserver.GrabResult{}, apiserver.ErrWorkNotWanted
	}
	var total apiserver.GrabResult
	for _, ref := range refs {
		result, err := c.grabOne(ctx, workID, apiserver.SearchScope{Group: &ref})
		if err != nil {
			return total, err
		}
		total.Found += result.Found
		total.Refused += result.Refused
		total.AlreadyDownloading = total.AlreadyDownloading || result.AlreadyDownloading
		total.Held = total.Held || result.Held
		if total.Sent == nil {
			total.Sent = result.Sent
		}
	}
	return total, nil
}

func (c CandidateSearch) grabOne(ctx context.Context, workID int64, scope apiserver.SearchScope) (apiserver.GrabResult, error) {
	searching, itemID, part, err := c.searching(ctx, workID, scope)
	if err != nil {
		return apiserver.GrabResult{}, err
	}
	found, best, err := searching.GrabWanted(ctx, media.WantedSearch{ItemID: itemID, Part: part})
	if err != nil {
		return apiserver.GrabResult{}, searchError(err)
	}
	view := apiserver.GrabResult{Found: len(found.Decisions), AlreadyDownloading: best.AlreadyInFlight, Held: best.Held, Refused: best.Refused}
	if best.Sent != nil {
		sent := acquisitionView(*best.Sent)
		view.Sent = &sent
	}
	return view, nil
}

// searchError maps a type's search errors to apiserver's.
func searchError(err error) error {
	switch {
	case errors.Is(err, media.ErrNoIndexers):
		return apiserver.ErrNoIndexersConfigured
	case errors.Is(err, media.ErrWholeItemOnly):
		return apiserver.ErrWholeItemOnly
	case errors.Is(err, media.ErrInvalidTerms):
		return fmt.Errorf("%w: %w", apiserver.ErrInvalidSearchTerms, err)
	}
	return translateDispatchError(err)
}

// BlocklistCandidate blocklists the release one of the scope's decisions recorded.
func (c CandidateSearch) BlocklistCandidate(ctx context.Context, workID int64, scope apiserver.SearchScope, decisionID int64) error {
	itemID, _, err := c.target(ctx, workID, scope)
	if errors.Is(err, apiserver.ErrWorkNotWanted) {
		return apiserver.ErrCandidateDecisionNotFound
	}
	if err != nil {
		return err
	}
	return translateDispatchError(c.Search.BlocklistRelease(ctx, itemID, decisionID, c.Clock.Now()))
}

// ListWorkCandidates answers the latest search's decisions for the scope's wanted item, best first: empty, not an
// error, for something never searched or never wanted.
func (c CandidateSearch) ListWorkCandidates(ctx context.Context, workID int64, scope apiserver.SearchScope) ([]apiserver.CandidateDecision, error) {
	itemID, _, err := c.target(ctx, workID, scope)
	if errors.Is(err, apiserver.ErrWorkNotWanted) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	details, err := c.Search.Candidates.ListDecisionsForItem(ctx, itemID, candidateHistoryLimit)
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

// SendCandidate dispatches decisionID, one of the scope's decisions, to a matching downloader (ADR-57's acquire.send).
func (c CandidateSearch) SendCandidate(ctx context.Context, workID int64, scope apiserver.SearchScope, decisionID int64) (apiserver.Acquisition, error) {
	itemID, _, err := c.target(ctx, workID, scope)
	if errors.Is(err, apiserver.ErrWorkNotWanted) {
		return apiserver.Acquisition{}, apiserver.ErrCandidateDecisionNotFound
	}
	if err != nil {
		return apiserver.Acquisition{}, err
	}
	acquisition, err := c.Dispatch.Send(ctx, itemID, decisionID, c.Clock.Now())
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
	case errors.Is(err, music.ErrContentBlocklisted):
		return apiserver.ErrContentBlocklisted
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
			// Only this library's own wanted albums: another library's work is not the caller's to search from here.
			if detail.Wanted != nil && slices.Contains(detail.CategoryIDs, categoryID) {
				itemIDs = append(itemIDs, detail.Wanted.ID)
			}
		}
		// No item ids would read as "the whole library" to the job; nothing named is wanted, so nothing is searched.
		if len(itemIDs) == 0 {
			return false, nil
		}
		enqueue = func() (jobs.EnqueueResult, error) { return music.EnqueueSearchItems(ctx, c.Queue, categoryID, itemIDs) }
	}
	result, err := enqueue()
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
}

// SearchCutoffUnmet enqueues the search of every album of categoryID that is below its cutoff.
func (c CandidateSearch) SearchCutoffUnmet(ctx context.Context, categoryID int64) (bool, error) {
	result, err := music.EnqueueSearchCutoffUnmet(ctx, c.Queue, categoryID)
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
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
	return c.sendableFirst(ctx, views)
}

// sendableFirst marks releases no enabled client can take and lists them after the rest, keeping
// the best-first order within each: a torrent ranked first with no torrent client misleads.
func (c CandidateSearch) sendableFirst(ctx context.Context, views []apiserver.CandidateDecision) []apiserver.CandidateDecision {
	if c.Dispatch.Downloaders == nil {
		return views
	}
	clients, err := c.Dispatch.Downloaders(ctx)
	if err != nil {
		return views
	}
	protocols := map[string]bool{}
	for _, client := range clients {
		protocols[client.Protocol] = true
	}
	for index := range views {
		views[index].NoClient = !protocols[views[index].Protocol]
	}
	slices.SortStableFunc(views, func(a, b apiserver.CandidateDecision) int {
		switch {
		case a.NoClient == b.NoClient:
			return 0
		case a.NoClient:
			return 1
		default:
			return -1
		}
	})
	return views
}

func (c CandidateSearch) searchItemResultView(ctx context.Context, result judge.Result) apiserver.SearchItemResult {
	decisions := c.decisionViews(ctx, result.Decisions)
	indexerErrors := make([]apiserver.IndexerSearchError, len(result.IndexerErrors))
	for index, indexerError := range result.IndexerErrors {
		indexerErrors[index] = apiserver.IndexerSearchError{
			IndexerID: indexerError.IndexerID, IndexerName: indexerError.IndexerName,
			Error: indexerError.Error, TooSlow: indexerError.TooSlow,
		}
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
