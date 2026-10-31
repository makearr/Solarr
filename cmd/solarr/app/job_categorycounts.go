package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// categoryCountsJobType is the registered type of the category-counter sweep.
const categoryCountsJobType = jobs.Type("library.reconcile_category_counts")

// categoryCountsActionKey deduplicates the sweep: global, not per-category, so at
// most one may be active at a time (Invariant 16), the same shape music.EnrichJob
// uses for its own global sweep.
const categoryCountsActionKey = "library.reconcile_category_counts"

var categoryCountsDefinition = jobs.Definition{
	Type: categoryCountsJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 3,
}

// categoryCountsJob recomputes every category's "works"/"files" counters
// (internal/db/library.CategoryCounters) from whichever categories and root folders
// settings.db currently declares -- wiring only, so it lives here rather than in
// internal/db/library, which does not know settings.db exists at all (ADR-25).
type categoryCountsJob struct {
	counters library.CategoryCounters
	cache    *settings.Cache
	logger   *slog.Logger
}

func (j categoryCountsJob) Run(ctx context.Context, _ jobs.Job) error {
	snapshot := j.cache.Snapshot()

	rootsByCategory := make(map[int64][]int64)
	for _, categoryRoot := range snapshot.CategoryRoots() {
		rootsByCategory[categoryRoot.CategoryID] = append(rootsByCategory[categoryRoot.CategoryID], categoryRoot.RootFolderID)
	}

	categories := snapshot.Categories()
	scopes := make([]library.CategoryScope, len(categories))
	for index, category := range categories {
		roots := rootsByCategory[category.ID]
		var offline []int64
		for _, rootFolderID := range roots {
			if root, found := snapshot.RootFolder(rootFolderID); found && !root.Online {
				offline = append(offline, rootFolderID)
			}
		}
		scopes[index] = library.CategoryScope{
			CategoryID: category.ID, RootFolderIDs: roots, OfflineRootFolderIDs: offline, MediaType: category.MediaType,
		}
	}

	corrections, err := j.counters.Reconcile(ctx, scopes)
	if err != nil {
		return fmt.Errorf("reconcile category counters: %w", err)
	}
	j.logger.Info("category counters reconciled",
		slog.Int("categories", len(scopes)), slog.Int("corrections", len(corrections)))
	return nil
}

// registerCategoryCountsJob wires the sweep into the registry and the executor.
func (p *process) registerCategoryCountsJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(categoryCountsDefinition); err != nil {
		return err
	}
	job := categoryCountsJob{
		counters: library.NewCategoryCounters(p.library, clock.NewSystem()),
		cache:    p.cache,
		logger:   p.logger,
	}
	executor.Handle(categoryCountsJobType, job)
	return nil
}

// enqueueCategoryCounts submits one sweep. Registered as a task with
// internal/jobs.TaskRunner (todo 4.19), so it also fires on its own
// schedule, not only once at startup. Normal priority: a few seconds of work that every count on
// screen waits for, which at low priority queued behind every pending rescan (2026-09-28).
func (p *process) enqueueCategoryCounts(ctx context.Context) (jobs.EnqueueResult, error) {
	result, err := p.queue.Enqueue(ctx, jobs.Request{
		Type: categoryCountsJobType, ActionKey: categoryCountsActionKey, Priority: jobs.PriorityNormal,
		Payload: []byte("{}"),
	})
	if err != nil {
		p.logger.Warn("enqueue category counters", slog.Any("error", err))
	}
	return result, err
}

// libraryIDsOf answers, each time it is asked, the libraries of mediaType as the settings hold them then.
func (p *process) libraryIDsOf(mediaType string) func() []int64 {
	return func() []int64 { return p.cache.Snapshot().CategoryIDsOf(mediaType) }
}
