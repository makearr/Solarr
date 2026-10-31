package adult

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/picturesweep"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/moving/renamejob"
	"github.com/makearr/Solarr/internal/platform/clock"
)

const (
	// activeRefresh and settledRefresh: a scene still to come weekly, a released one every three months, as its facts
	// rarely change once it is out.
	activeRefresh  = 7 * 24 * time.Hour
	settledRefresh = 90 * 24 * time.Hour
	// refreshPerRun bounds one sweep so a large library is spread over the day's runs.
	refreshPerRun = 60
)

// RefreshJobType is the registered type of the refresh sweep.
const RefreshJobType jobs.Type = "adult.refresh_scenes"

// ArtworkJobType is the registered type of the sweep that keeps a library's posters and performer photos at hand.
const ArtworkJobType jobs.Type = "adult.artwork"

// RefreshDefinition declares the sweep: background metadata work, retried on transient failure.
func RefreshDefinition() jobs.Definition {
	return jobs.Definition{Name: "Refreshing scenes", Type: RefreshJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3}
}

// EnqueueRefresh submits one refresh sweep, one at a time (Invariant 16).
func EnqueueRefresh(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: RefreshJobType, ActionKey: string(RefreshJobType), Priority: jobs.PriorityLow, Payload: []byte("{}")})
}

// NewRefreshJob is the shared refresh sweep over the scenes that are due, so new tags, performers and pictures reach the
// library.
func NewRefreshJob(reader library.SeriesReader, service Service, clock clock.Clock, logger *slog.Logger) works.RefreshJob {
	return works.RefreshJob{
		Name: MediaType, JobType: RefreshJobType, NoSource: ErrNoSource, Clock: clock, Logger: logger,
		Due: func(ctx context.Context) ([]library.RefreshCandidate, error) {
			return reader.DueForRefreshOf(ctx, MediaType, string(StatusReleased), clock.Now(), activeRefresh, settledRefresh, refreshPerRun)
		},
		Refresh: func(ctx context.Context, candidate library.RefreshCandidate) error {
			_, err := service.Refresh(ctx, candidate.CategoryID, candidate.WorkID)
			return err
		},
		MarkAttempt: func(ctx context.Context, workID int64) error {
			return service.Series.MarkRefreshAttempt(ctx, clock.Now(), workID)
		},
	}
}

// ArtworkDefinition declares the picture sweep.
func ArtworkDefinition() jobs.Definition {
	return jobs.Definition{Name: "Fetching scene posters", Type: ArtworkJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1}
}

// NewArtworkJob keeps the posters, studio logos and performer photos in the picture cache.
func NewArtworkJob(reader picturesweep.Reader, cache picturesweep.Cache, logger *slog.Logger) *picturesweep.Job {
	return picturesweep.New(picturesweep.Config{MediaType: MediaType, OrganisationKind: "studio", Reader: reader, Cache: cache, Logger: logger})
}

// EnqueueArtwork submits one picture sweep.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: ArtworkJobType, ActionKey: string(ArtworkJobType), Priority: jobs.PriorityLow})
}

// RenameConfig is what naming a library's scene files needs; renamejob.Engine does the rest.
type RenameConfig struct {
	Reader library.SeriesReader
	Clock  clock.Clock
}

// NameFiles is the module's part of a rename: each file the whole scene, a second one numbered as its part.
func (cfg RenameConfig) NameFiles(ctx context.Context, category settings.Category, workID int64, files []renamejob.File) ([]renamejob.Named, error) {
	facts, err := NamingFacts(ctx, cfg.Reader, category.ID, workID, cfg.Clock.Now())
	if err != nil {
		return nil, err
	}
	names := NamingFor(category.Naming)
	ordered := slices.Clone(files)
	slices.SortStableFunc(ordered, func(a, b renamejob.File) int { return strings.Compare(a.RelPath, b.RelPath) })
	named := make([]renamejob.Named, 0, len(ordered))
	taken := map[string]bool{}
	for index, file := range ordered {
		facts := facts
		facts.PartNumber, facts.PartCount = index+1, len(ordered)
		rendered, warnings, err := names.RenderScene(facts, file.RelPath)
		if err != nil || taken[rendered] {
			continue
		}
		taken[rendered] = true
		named = append(named, renamejob.Named{MediaObjectID: file.MediaObjectID, RelPath: rendered, Warnings: warnings})
	}
	return named, nil
}
