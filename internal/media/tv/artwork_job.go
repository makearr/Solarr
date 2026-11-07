package tv

import (
	"context"
	"log/slog"

	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/picturesweep"
	"github.com/makearr/Solarr/internal/media/works"
)

// ArtworkJobType is the registered type of the sweep that keeps a library's pictures at hand.
const ArtworkJobType = jobs.Type("tv.artwork")

// ArtworkDefinition declares the sweep: background metadata work, resumed by the next run.
var ArtworkDefinition = works.ArtworkDefinition("Fetching series pictures", ArtworkJobType)

// NewArtworkJob keeps TV libraries' series posters, network logos and billed-first cast photos in the picture cache.
func NewArtworkJob(reader picturesweep.Reader, cache picturesweep.Cache, logger *slog.Logger) *picturesweep.Job {
	return picturesweep.New(picturesweep.Config{MediaType: MediaType, OrganisationKind: "network", Reader: reader, Cache: cache, Logger: logger})
}

// EnqueueArtwork submits one sweep, deduplicated by ArtworkActionKey.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return works.EnqueueArtwork(ctx, queue, ArtworkJobType)
}
