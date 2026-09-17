package music

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/artwork"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// ArtworkJobType is the registered type of the background cover art sweep.
const ArtworkJobType = jobs.Type("music.artwork")

// ArtworkActionKey deduplicates the sweep the same way EnrichActionKey does: at
// most one may be active at a time (Invariant 16).
const ArtworkActionKey = "music.artwork"

// ArtworkDefinition is LOW priority (section 21.6): it must never compete with
// import or acquisition for the write queue or the network.
var ArtworkDefinition = jobs.Definition{
	Type: ArtworkJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// artworkBatchSize mirrors enrichBatchSize's reasoning: small enough that a
// shutdown between batches loses at most one batch's worth of otherwise-committed
// progress.
const artworkBatchSize = 20

// maxImageBytes bounds one fetched cover image. Cover Art Archive uploads are
// typically well under this; a provider serving something far larger is a
// reason to stop reading, not a reason to buffer it all first.
const maxImageBytes = 20 << 20

// CoverArtFetcher answers the best source image URL for a release, or
// musicbrainz.ErrNoCoverArt when there is none.
type CoverArtFetcher interface {
	Fetch(ctx context.Context, releaseMBID string) (string, error)
}

// ArtworkJob fetches, resizes and encodes cover art for every work that has a
// usable release MBID and none recorded yet (section 21.6). Direct-wired the
// same way the rest of internal/media/music is until stage 9.
type ArtworkJob struct {
	artworks  library.Artworks
	coverArt  CoverArtFetcher
	http      *httpx.Client
	fs        fsx.FS
	imagesDir string
	clock     clock.Clock
	logger    *slog.Logger
}

// ArtworkJobConfig is what an ArtworkJob needs. Every field is required except Logger.
type ArtworkJobConfig struct {
	Artworks  library.Artworks
	CoverArt  CoverArtFetcher
	HTTP      *httpx.Client
	FS        fsx.FS
	ImagesDir string
	Clock     clock.Clock
	Logger    *slog.Logger
}

// NewArtworkJob builds the handler.
func NewArtworkJob(config ArtworkJobConfig) *ArtworkJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ArtworkJob{
		artworks: config.Artworks, coverArt: config.CoverArt, http: config.HTTP,
		fs: config.FS, imagesDir: config.ImagesDir, clock: config.Clock, logger: logger,
	}
}

// Run processes every work it can find needing cover art, in bounded batches,
// until none remain or the context is cancelled. No persisted cursor: a
// recorded work stops being returned by NeedingArtwork, so a restart simply
// asks the same question again (EnrichJob's Run doc comment, same reasoning).
func (j *ArtworkJob) Run(ctx context.Context, _ jobs.Job) error {
	if err := j.fs.MkdirAll(j.imagesDir, 0o755); err != nil {
		return fmt.Errorf("music: artwork: create %s: %w", j.imagesDir, err)
	}

	total, failed, skipped := 0, 0, 0
	// Walked by work id: a release with no archive art records nothing, so
	// re-reading from the top would return the same page indefinitely.
	var afterWorkID int64
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("artwork.context", string(ArtworkJobType), err)
		}

		batch, err := j.artworks.NeedingArtwork(ctx, afterWorkID, artworkBatchSize)
		if err != nil {
			return fmt.Errorf("music: artwork: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, candidate := range batch {
			total++
			afterWorkID = candidate.WorkID
			switch err := j.processOne(ctx, candidate); {
			case errors.Is(err, musicbrainz.ErrNoCoverArt):
				// Not a failure: this release genuinely has no art. Recorded
				// nowhere, so it is asked about again next sweep -- cheap
				// (one API call) and correct if art is added to the archive later.
				skipped++
			case err != nil:
				failed++
				j.logger.WarnContext(ctx, "cover art fetch failed",
					slog.Int64("work_id", candidate.WorkID), slog.Any("error", err))
			}
		}
		if len(batch) < artworkBatchSize {
			break
		}
	}
	j.logger.Info("music artwork sweep finished",
		slog.Int("processed", total), slog.Int("failed", failed), slog.Int("skipped", skipped))
	return nil
}

func (j *ArtworkJob) processOne(ctx context.Context, candidate library.ArtworkCandidate) error {
	imageURL, err := j.coverArt.Fetch(ctx, candidate.ReleaseMBID)
	if err != nil {
		return err
	}

	response, err := j.http.Get(ctx, imageURL)
	if err != nil {
		return fmt.Errorf("fetch image: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch image: status %d", response.StatusCode)
	}
	source, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes))
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	sizes, err := artwork.Process(source)
	if err != nil {
		return fmt.Errorf("process image: %w", err)
	}

	cacheKey := fmt.Sprintf("work-%d-cover", candidate.WorkID)
	for size, image := range sizes {
		destination := filepath.Join(j.imagesDir, fmt.Sprintf("%s-%s.webp", cacheKey, artworkSizeName(size)))
		if err := fsx.WriteAtomic(j.fs, destination, func(w io.Writer) error {
			_, err := w.Write(image.Bytes)
			return err
		}); err != nil {
			return fmt.Errorf("write %s: %w", destination, err)
		}
	}

	full := sizes[artwork.Full]
	return j.artworks.Record(ctx, candidate.WorkID, imageURL, cacheKey, full.Width, full.Height, j.clock.Now().Unix())
}

func artworkSizeName(size artwork.Size) string {
	switch size {
	case artwork.Thumb:
		return "thumb"
	case artwork.Small:
		return "small"
	default:
		return "full"
	}
}

// EnqueueArtwork submits one sweep, deduplicated by ArtworkActionKey.
func EnqueueArtwork(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: ArtworkJobType, ActionKey: ArtworkActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
