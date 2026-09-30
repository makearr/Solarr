package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// SearchCategoryJobType is the registered type of a per-category candidate
// search run (the "search now" action on a category, todo 5.2's own trigger
// scope).
const SearchCategoryJobType = jobs.Type("music.search_category")

// SearchCategoryDefinition declares how the job behaves: background,
// mutating (it writes candidate/candidate_decision rows), retried once on a
// transient failure -- a whole category re-search is not cheap enough to
// retry as aggressively as a single item would be.
var SearchCategoryDefinition = jobs.Definition{
	Name: "Searching wanted albums",
	Type: SearchCategoryJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

// SearchCategoryActionKey deduplicates concurrent runs for the same
// category -- a second "search now" click while one is already running
// enqueues nothing new, the same reasoning importjob.ActionKeyFor already
// gives per source path.
func SearchCategoryActionKey(categoryID int64) string {
	return fmt.Sprintf("%s:%d", SearchCategoryJobType, categoryID)
}

// SearchCategoryPayload is what one run carries: which category to search, and optionally which
// artist in it (the artist page's "search all missing").
type SearchCategoryPayload struct {
	CategoryID int64 `json:"category_id"`
	PersonID   int64 `json:"person_id,omitempty"`
	// ItemIDs searches exactly these albums: the Wanted page's "search this page".
	ItemIDs []int64 `json:"item_ids,omitempty"`
}

// EnqueueSearchCategory submits one category's own search run.
func EnqueueSearchCategory(ctx context.Context, queue *jobs.Queue, categoryID int64) (jobs.EnqueueResult, error) {
	return enqueueSearch(ctx, queue, SearchCategoryPayload{CategoryID: categoryID}, SearchCategoryActionKey(categoryID))
}

// EnqueueSearchItems searches exactly these albums of one library, one after another.
func EnqueueSearchItems(ctx context.Context, queue *jobs.Queue, categoryID int64, itemIDs []int64) (jobs.EnqueueResult, error) {
	return enqueueSearch(ctx, queue, SearchCategoryPayload{CategoryID: categoryID, ItemIDs: itemIDs},
		fmt.Sprintf("%s:%d:items:%v", SearchCategoryJobType, categoryID, itemIDs))
}

// EnqueueSearchArtist searches one artist's missing albums; zero categoryID means every library.
func EnqueueSearchArtist(ctx context.Context, queue *jobs.Queue, categoryID, personID int64) (jobs.EnqueueResult, error) {
	return enqueueSearch(ctx, queue, SearchCategoryPayload{CategoryID: categoryID, PersonID: personID},
		fmt.Sprintf("%s:%d:person:%d", SearchCategoryJobType, categoryID, personID))
}

func enqueueSearch(ctx context.Context, queue *jobs.Queue, payload SearchCategoryPayload, actionKey string) (jobs.EnqueueResult, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("music: encode search category payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: SearchCategoryJobType, ActionKey: actionKey,
		Priority: jobs.PriorityLow, Payload: data, CategoryID: payload.CategoryID,
	})
}

// searchCategoryPageSize bounds one page of IncompleteItems -- the same
// pagination shape library health's own listing already uses, so a category
// with many wanted albums is walked in bounded batches rather than one
// unbounded query.
const searchCategoryPageSize = 50

// IndexerSource resolves the currently configured, enabled indexers fresh
// on every run -- settings can change between runs, the same "ask fresh,
// never cache" discipline every settings-backed job dependency in this
// codebase already follows (moverjob.RootFolders, ArrSync's own Cache
// reads).
type IndexerSource func(ctx context.Context) ([]IndexerConn, error)

// SearchCategoryJob runs SearchItem across every monitored, incomplete
// album-level item in one category. Track-level items in the same category
// (ErrTrackSearchNotSupported) are skipped, not failed -- this pass's own
// scope, not a defect in the item. One item failing does not stop the rest,
// the same per-item isolation SubscriptionExpandJob's own Run already uses.
type SearchCategoryJob struct {
	grab     Grab
	indexers IndexerSource
	clock    clock.Clock
	logger   *slog.Logger
}

// SearchCategoryConfig is what a SearchCategoryJob needs. Every field is
// required except Logger.
type SearchCategoryConfig struct {
	// Grab sends each album's best release, as Lidarr's "Search all" does (2026-09-25).
	Grab     Grab
	Indexers IndexerSource
	Clock    clock.Clock
	Logger   *slog.Logger
}

// NewSearchCategoryJob builds the handler.
func NewSearchCategoryJob(config SearchCategoryConfig) *SearchCategoryJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SearchCategoryJob{grab: config.Grab, indexers: config.Indexers, clock: config.Clock, logger: logger}
}

// Run walks every monitored MISSING/PARTIAL item in the category once,
// searching each in turn. Results are visible per work through
// Candidates.ListDecisionsForItem -- this run itself reports only a summary
// to the log, the same level of detail SubscriptionExpandJob's own Run logs.
func (j *SearchCategoryJob) Run(ctx context.Context, job jobs.Job) error {
	var payload SearchCategoryPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("music: search category: decode payload: %w", err)
	}

	indexers, err := j.indexers(ctx)
	if err != nil {
		return fmt.Errorf("music: search category %d: indexers: %w", payload.CategoryID, err)
	}
	if len(indexers) == 0 {
		j.logger.Warn("music: search category: no enabled indexers configured", "category_id", payload.CategoryID)
		return nil
	}

	now := j.clock.Now()
	tally := searchTally{progress: job.Progress}
	if len(payload.ItemIDs) > 0 {
		job.Progress.SetTotal(int64(len(payload.ItemIDs)))
		for _, itemID := range payload.ItemIDs {
			if err := ctx.Err(); err != nil {
				return err
			}
			j.grabOne(ctx, payload.CategoryID, itemID, indexers, now, &tally)
		}
	} else if err := j.grabIncomplete(ctx, payload, indexers, now, &tally); err != nil {
		return err
	}

	j.logger.Info("music: search category: done", "category_id", payload.CategoryID,
		"searched", tally.searched, "found", tally.found, "sent", tally.sent, "skipped", tally.skipped,
		"failed", tally.failed)
	return nil
}

// searchTally is what one run did, for its log line.
type searchTally struct {
	searched, found, sent, skipped, failed int
	// progress counts albums searched for the status bar.
	progress *jobs.Progress
}

func (j *SearchCategoryJob) grabIncomplete(
	ctx context.Context, payload SearchCategoryPayload, indexers []IndexerConn, now time.Time, tally *searchTally,
) error {
	afterID := int64(0)
	for {
		items, err := j.grab.Search.Desired.IncompleteItems(ctx, payload.CategoryID, payload.PersonID, afterID, searchCategoryPageSize)
		if err != nil {
			return fmt.Errorf("music: search category %d: list items: %w", payload.CategoryID, err)
		}
		for _, item := range items {
			afterID = item.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			j.grabOne(ctx, payload.CategoryID, item.ID, indexers, now, tally)
		}
		if len(items) < searchCategoryPageSize {
			return nil
		}
	}
}

func (j *SearchCategoryJob) grabOne(
	ctx context.Context, categoryID, itemID int64, indexers []IndexerConn, now time.Time, tally *searchTally,
) {
	result, err := j.grab.SearchAndSend(ctx, itemID, indexers, GrabAutomatic, now)
	tally.progress.Add(1)
	switch {
	case errors.Is(err, ErrTrackSearchNotSupported), result.AlreadyInFlight:
		tally.skipped++
	case err != nil:
		tally.failed++
		j.logger.Warn("music: search category: item failed", "category_id", categoryID, "item_id", itemID, "error", err)
	default:
		tally.searched++
		if result.Search.TierUsed != nil {
			tally.found++
		}
		if result.Sent != nil {
			tally.sent++
		}
		if len(result.Refusals) > 0 {
			j.logger.Info("music: search category: releases refused", "item_id", itemID, "refused", result.Refusals)
		}
	}
}
