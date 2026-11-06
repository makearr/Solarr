// Package franchiselink links the collections of different libraries that a provider puts in one franchise (Star Wars as
// films, series and soundtracks), whatever the media types are; a link a person undid is never made again.
package franchiselink

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// JobType is the pass that links collections across libraries.
const JobType = jobs.Type("franchise.link")

// ActionKey deduplicates the pass (Invariant 16).
const ActionKey = "franchise.link"

// Definition declares the pass: housekeeping over franchise ids already held.
var Definition = jobs.Definition{
	Name: "Linking the collections of different libraries", Type: JobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 2,
}

// Store is the collections the pass reads and links.
type Store interface {
	LinkCandidates(ctx context.Context) ([]library.LinkCandidate, error)
	LinkState(ctx context.Context) (map[[2]int64]bool, map[[2]string]bool, error)
	AutoLink(ctx context.Context, link library.PlannedLink) (bool, error)
}

// Service is the pass's handler.
type Service struct {
	Store  Store
	Logger *slog.Logger
}

// Run implements jobs.Handler.
func (s Service) Run(ctx context.Context, _ jobs.Job) error {
	linked, err := s.Link(ctx)
	if err != nil {
		return err
	}
	s.Logger.Info("collection link pass finished", slog.Int("linked", linked))
	return nil
}

// Link applies the rule once and answers how many pairs it linked.
func (s Service) Link(ctx context.Context) (int, error) {
	candidates, err := s.Store.LinkCandidates(ctx)
	if err != nil {
		return 0, err
	}
	linked, refused, err := s.Store.LinkState(ctx)
	if err != nil {
		return 0, err
	}
	made := 0
	for _, planned := range library.PlanCollectionLinks(candidates, linked, refused) {
		ok, err := s.Store.AutoLink(ctx, planned)
		if err != nil {
			return made, err
		}
		if ok {
			made++
		}
	}
	return made, nil
}

// Enqueue submits one pass.
func Enqueue(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: JobType, ActionKey: ActionKey, Priority: jobs.PriorityLow})
}
