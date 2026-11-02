// Package franchisewatch adds what a monitored franchise gains later, for every media type that groups works into franchises.
package franchisewatch

import (
	"context"
	"errors"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
)

// JobType is the sweep that adds the newly listed works of every monitored franchise.
const JobType = jobs.Type("franchise.expand")

// ActionKey deduplicates the sweep (Invariant 16).
const ActionKey = "franchise.expand"

// AddLimit bounds the works one sweep adds; the rest follow on the next run, as a follow's limit does.
const AddLimit = 25

// Definition declares the sweep: background metadata work that adds works, retried on transient failure.
var Definition = jobs.Definition{
	Name: "Adding the new works of monitored franchises", Type: JobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// Libraries lists the libraries (categories) in the order they are shown.
type Libraries interface {
	Categories() []settings.Category
}

// Franchises lists a library's collections.
type Franchises interface {
	InCategory(ctx context.Context, mediaType string, categoryID int64) ([]library.FranchiseRow, error)
}

// Service is the sweep's handler.
type Service struct {
	Libraries  Libraries
	Franchises Franchises
	Media      *media.Registry
	Logger     *slog.Logger
}

func (s Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// Run asks the media type of each library to follow again every provider franchise a person monitors; a franchise is
// handled once, in the first library holding it, and one failing never stops the others.
func (s Service) Run(ctx context.Context, _ jobs.Job) error {
	added, failed := s.sweep(ctx)
	s.logger().Info("franchise sweep finished", slog.Int("added", added), slog.Int("failed", failed))
	return nil
}

func (s Service) sweep(ctx context.Context) (added, failed int) {
	done := map[int64]bool{}
	for _, category := range s.Libraries.Categories() {
		provider, ok := media.Capability[media.Franchising](s.Media, media.Key(category.MediaType))
		if !ok {
			continue
		}
		rows, err := s.Franchises.InCategory(ctx, category.MediaType, category.ID)
		if err != nil {
			s.logger().WarnContext(ctx, "franchise sweep could not list a library", slog.Int64("category_id", category.ID), slog.Any("error", err))
			failed++
			continue
		}
		for _, row := range rows {
			if ctx.Err() != nil || added >= AddLimit {
				return added, failed
			}
			if row.Kind != library.FranchiseKind || !row.Monitored || done[row.ID] {
				continue
			}
			done[row.ID] = true
			count, err := provider.MonitorFranchise(ctx, category.ID, row.ID, true)
			added += count
			if err != nil && !errors.Is(err, media.ErrNotWired) {
				failed++
				s.logger().WarnContext(ctx, "franchise could not be followed", slog.Int64("franchise_id", row.ID), slog.Any("error", err))
			}
		}
	}
	return added, failed
}

// Enqueue submits the sweep.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow})
}
