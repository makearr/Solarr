package music

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/platform/clock"
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
	Type: SearchCategoryJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 2,
}

// SearchCategoryActionKey deduplicates concurrent runs for the same
// category -- a second "search now" click while one is already running
// enqueues nothing new, the same reasoning importjob.ActionKeyFor already
// gives per source path.
func SearchCategoryActionKey(categoryID int64) string {
	return fmt.Sprintf("%s:%d", SearchCategoryJobType, categoryID)
}

// SearchCategoryPayload is what one run carries: which category to search.
type SearchCategoryPayload struct {
	CategoryID int64 `json:"category_id"`
}

// EnqueueSearchCategory submits one category's own search run.
func EnqueueSearchCategory(ctx context.Context, queue *jobs.Queue, categoryID int64) (jobs.EnqueueResult, error) {
	data, err := json.Marshal(SearchCategoryPayload{CategoryID: categoryID})
	if err != nil {
		return jobs.EnqueueResult{}, fmt.Errorf("music: encode search category payload: %w", err)
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: SearchCategoryJobType, ActionKey: SearchCategoryActionKey(categoryID),
		Priority: jobs.PriorityLow, Payload: data, CategoryID: categoryID,
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
	search   Search
	indexers IndexerSource
	clock    clock.Clock
	logger   *slog.Logger
}

// SearchCategoryConfig is what a SearchCategoryJob needs. Every field is
// required except Logger.
type SearchCategoryConfig struct {
	Search   Search
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
	return &SearchCategoryJob{search: config.Search, indexers: config.Indexers, clock: config.Clock, logger: logger}
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
	var searched, found, skipped, failed int
	afterID := int64(0)
	for {
		items, err := j.search.Desired.IncompleteItems(ctx, payload.CategoryID, afterID, searchCategoryPageSize)
		if err != nil {
			return fmt.Errorf("music: search category %d: list items: %w", payload.CategoryID, err)
		}
		if len(items) == 0 {
			break
		}
		for _, item := range items {
			afterID = item.ID
			if err := ctx.Err(); err != nil {
				return err
			}
			result, itemErr := j.search.SearchItem(ctx, item.ID, indexers, now)
			switch {
			case errors.Is(itemErr, ErrTrackSearchNotSupported):
				skipped++
			case itemErr != nil:
				failed++
				j.logger.Warn("music: search category: item failed",
					"category_id", payload.CategoryID, "item_id", item.ID, "error", itemErr)
			default:
				searched++
				if result.TierUsed != nil {
					found++
				}
			}
		}
		if len(items) < searchCategoryPageSize {
			break
		}
	}

	j.logger.Info("music: search category: done", "category_id", payload.CategoryID,
		"searched", searched, "found", found, "skipped", skipped, "failed", failed)
	return nil
}
