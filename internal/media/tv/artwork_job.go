package tv

import (
	"context"
	"log/slog"
	"sync"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
)

// ArtworkJobType is the registered type of the sweep that keeps a library's pictures at hand.
const ArtworkJobType = jobs.Type("tv.artwork")

// ArtworkActionKey deduplicates the sweep: one at a time (Invariant 16).
const ArtworkActionKey = "tv.artwork"

// ArtworkDefinition declares the sweep: background metadata work, resumed by the next run.
var ArtworkDefinition = jobs.Definition{
	Name: "Fetching series pictures",
	Type: ArtworkJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 1,
}

const (
	// artworkPerRun bounds one run so a first pass over a big library is spread over several.
	artworkPerRun = 1500
	// artworkWorkers fetch side by side: a provider's CDN answers in about 0.7 s, so one at a time is seven posters a second at best.
	artworkWorkers = 6
	// maxPhotos bounds the people whose photo is kept ready: the billed-first of each series.
	maxPhotos = 6000
)

// PictureCache is the provider picture cache as the sweep needs it.
type PictureCache interface {
	Cached(address string) bool
	Ensure(ctx context.Context, address string) (string, error)
}

// ArtworkConfig is what an ArtworkJob needs; Logger is optional.
type ArtworkConfig struct {
	Reader library.SeriesReader
	Cache  PictureCache
	Logger *slog.Logger
}

// ArtworkJob fetches the posters, network logos and first-billed photos a TV library shows before anyone scrolls to them,
// the way the music library's covers are ready: a poster fetched when it is first seen costs the viewer the CDN's latency.
type ArtworkJob struct {
	config ArtworkConfig
	logger *slog.Logger
}

// NewArtworkJob builds the handler.
func NewArtworkJob(config ArtworkConfig) *ArtworkJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ArtworkJob{config: config, logger: logger}
}

// Run fetches what is missing, posters first.
func (j *ArtworkJob) Run(ctx context.Context, _ jobs.Job) error {
	set, err := j.config.Reader.PictureAddresses(ctx, maxPhotos)
	if err != nil {
		return err
	}
	var missing []string
	for _, group := range [][]string{set.Posters, set.Logos, set.Photos} {
		for _, address := range group {
			if !j.config.Cache.Cached(address) {
				missing = append(missing, address)
			}
		}
	}
	total := len(missing)
	if len(missing) > artworkPerRun {
		missing = missing[:artworkPerRun]
	}
	var (
		wait         sync.WaitGroup
		lock         sync.Mutex
		fetched, bad int
		queue        = make(chan string)
	)
	for range artworkWorkers {
		wait.Go(func() {
			for address := range queue {
				_, err := j.config.Cache.Ensure(ctx, address)
				lock.Lock()
				if err != nil {
					bad++
				} else {
					fetched++
				}
				lock.Unlock()
			}
		})
	}
	for _, address := range missing {
		if ctx.Err() != nil {
			break
		}
		queue <- address
	}
	close(queue)
	wait.Wait()
	j.logger.Info("series pictures fetched", slog.Int("fetched", fetched), slog.Int("failed", bad), slog.Int("still_missing", total-fetched-bad))
	return ctx.Err()
}

// EnqueueArtwork submits one sweep, deduplicated by ArtworkActionKey.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{Type: ArtworkJobType, ActionKey: ArtworkActionKey, Priority: jobs.PriorityLow})
}
