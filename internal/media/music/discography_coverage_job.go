package music

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// DiscographyCoverageJobType rolls up how much of each held artist's discography a library holds (revision 6.4.5).
const DiscographyCoverageJobType = jobs.Type("music.discography_coverage")

// DiscographyCoverageDefinition runs in the background: it asks MusicBrainz for each artist's discography,
// which the provider caches, so only the first run costs requests.
var DiscographyCoverageDefinition = jobs.Definition{
	Name: "Comparing discographies",
	Type: DiscographyCoverageJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1,
}

// DiscographyProvider answers an artist's release groups, cache first.
type DiscographyProvider interface {
	ArtistReleaseGroups(ctx context.Context, priority musicbrainz.Priority, artistMBID string) ([]musicbrainz.ReleaseGroup, error)
}

// DiscographyCoverageConfig is what the job needs.
type DiscographyCoverageConfig struct {
	Statistics library.Statistics
	// Coverage keeps what the comparison found, in statistics.db.
	Coverage statistics.Rollup
	Provider DiscographyProvider
	// Categories lists the music libraries to compare.
	Categories func() []int64
	Clock      clock.Clock
	Logger     *slog.Logger
}

// DiscographyCoverageJob compares, per library, each artist's held albums with the albums their discography has
// under the default subscription filters.
type DiscographyCoverageJob struct{ config DiscographyCoverageConfig }

// NewDiscographyCoverageJob builds it.
func NewDiscographyCoverageJob(config DiscographyCoverageConfig) *DiscographyCoverageJob {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	return &DiscographyCoverageJob{config: config}
}

// Run rebuilds each library's coverage rows; an artist whose discography cannot be read is left out this time.
func (j *DiscographyCoverageJob) Run(ctx context.Context, job jobs.Job) error {
	filters := ParseSubscriptionFilters("")
	for _, categoryID := range j.config.Categories() {
		artists, err := j.config.Statistics.ArtistsHeld(ctx, categoryID)
		if err != nil {
			return err
		}
		job.Progress.SetTotal(int64(len(artists)))
		rows := make([]statistics.DiscographyCoverage, 0, len(artists))
		failed := 0
		for _, artist := range artists {
			if err := ctx.Err(); err != nil {
				return err
			}
			groups, err := j.config.Provider.ArtistReleaseGroups(ctx, musicbrainz.PriorityBackfill, artist.ArtistMBID)
			job.Progress.Add(1)
			if err != nil {
				failed++
				j.config.Logger.Debug("discography coverage: artist skipped", "person_id", artist.PersonID, "error", err)
				continue
			}
			var total, held int64
			for _, group := range groups {
				if !filters.Admits(group) {
					continue
				}
				total++
				if slices.Contains(artist.ReleaseGroups, group.MBID) {
					held++
				}
			}
			rows = append(rows, statistics.DiscographyCoverage{PersonID: artist.PersonID, Held: held, Total: total})
		}
		if err := j.config.Coverage.SetDiscographyCoverage(ctx, categoryID, rows, j.config.Clock.Now().Unix()); err != nil {
			return fmt.Errorf("music: discography coverage: %w", err)
		}
		j.config.Logger.Info("music: discography coverage done", "category_id", categoryID, "artists", len(rows), "skipped", failed)
	}
	return nil
}
