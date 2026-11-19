// Package franchisefacts fills in the picture and work count a provider holds for a collection, off the request that lists it.
package franchisefacts

import (
	"context"
	"log/slog"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// JobType is the pass that asks the providers for what collections lack.
const JobType = jobs.Type("franchise.facts")

// ActionKey deduplicates the pass (Invariant 16).
const ActionKey = "franchise.facts"

// PerLibrary bounds the collections one pass asks about in a library; the rest follow on the next run.
const PerLibrary = 15

// RecheckAfter is how long a collection the provider could tell nothing about is left alone.
const RecheckAfter = 7 * 24 * time.Hour

// Definition declares the pass: background metadata work, retried on transient failure.
var Definition = jobs.Definition{
	Name: "Filling in collection pictures and counts", Type: JobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// Libraries lists the libraries (categories) in the order they are shown.
type Libraries interface {
	Categories() []settings.Category
}

// Franchises is the collections store: listing, and keeping what a provider said.
type Franchises interface {
	InCategory(ctx context.Context, mediaType string, categoryID int64) ([]library.FranchiseRow, error)
	SetTotal(ctx context.Context, id, total int64) error
	SetPoster(ctx context.Context, id int64, posterURL string) error
	SetFactsChecked(ctx context.Context, id, checkedAt int64) error
}

// Service is the pass's handler.
type Service struct {
	Libraries  Libraries
	Franchises Franchises
	Media      *media.Registry
	Clock      clock.Clock
	Logger     *slog.Logger
}

// Run asks each library's media type for the facts of the collections still missing a picture or a count and not asked
// within RecheckAfter, and stamps every one asked, so none is asked on every pass.
func (s Service) Run(ctx context.Context, _ jobs.Job) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := s.Clock.Now()
	filled := 0
	for _, category := range s.Libraries.Categories() {
		provider, ok := media.Capability[media.Franchising](s.Media, media.Key(category.MediaType))
		if !ok {
			continue
		}
		rows, err := s.Franchises.InCategory(ctx, category.MediaType, category.ID)
		if err != nil {
			logger.WarnContext(ctx, "collection facts could not list a library", slog.Int64("category_id", category.ID), slog.Any("error", err))
			continue
		}
		var asked []media.FranchiseRef
		byID := map[int64]library.FranchiseRow{}
		for _, row := range rows {
			due := time.Unix(row.FactsCheckedAt, 0).Add(RecheckAfter).Before(now)
			if row.Kind == library.FranchiseKind && (row.PosterURL == "" || row.Total == 0) && due && len(asked) < PerLibrary {
				asked = append(asked, media.FranchiseRef{ID: row.ID, ExternalID: row.ExternalID})
				byID[row.ID] = row
			}
		}
		if len(asked) == 0 {
			continue
		}
		facts := provider.FranchiseFacts(ctx, asked)
		for _, ref := range asked {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fact, row := facts[ref.ID], byID[ref.ID]
			if fact.Total > 0 && int64(fact.Total) != row.Total {
				if err := s.Franchises.SetTotal(ctx, ref.ID, int64(fact.Total)); err == nil {
					filled++
				}
			}
			if fact.PosterURL != "" && row.PosterURL == "" {
				if err := s.Franchises.SetPoster(ctx, ref.ID, fact.PosterURL); err == nil {
					filled++
				}
			}
			if err := s.Franchises.SetFactsChecked(ctx, ref.ID, now.Unix()); err != nil {
				logger.WarnContext(ctx, "collection facts check not stamped", slog.Int64("franchise_id", ref.ID), slog.Any("error", err))
			}
		}
	}
	logger.Info("collection facts pass finished", slog.Int("filled", filled))
	return nil
}

// Enqueue submits the pass.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow})
}
