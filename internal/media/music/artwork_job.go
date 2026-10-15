package music

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// ArtworkJobType is the registered type of the background cover art sweep.
const ArtworkJobType = jobs.Type("music.artwork")

// ArtworkActionKey deduplicates the sweep the same way EnrichActionKey does: at
// most one may be active at a time (Invariant 16).
const ArtworkActionKey = "music.artwork"

// ArtworkDefinition is LOW priority (section 21.6): it must never compete with
// import or acquisition for the write queue or the network.
var ArtworkDefinition = jobs.Definition{
	Name: "Fetching cover art",
	Type: ArtworkJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// artworkBatchSize mirrors enrichBatchSize's reasoning: small enough that a
// shutdown between batches loses at most one batch's worth of otherwise-committed
// progress.
const artworkBatchSize = 20

// artworkWorkers overlaps a batch's covers: each is a lookup plus a download of several megabytes,
// and one at a time the full library gained ~500 covers an hour (measured 2026-09-28).
const artworkWorkers = 4

// maxImageBytes bounds one fetched cover image. Cover Art Archive uploads are
// typically well under this; a provider serving something far larger is a
// reason to stop reading, not a reason to buffer it all first.
const maxImageBytes = 20 << 20

// CoverArtFetcher answers the best source image URL for a release, or
// musicbrainz.ErrNoCoverArt when there is none.
type CoverArtFetcher interface {
	Fetch(ctx context.Context, releaseMBID string) (string, error)
	FetchReleaseGroup(ctx context.Context, releaseGroupMBID string) (string, error)
}

// ArtworkJob fetches, resizes and encodes cover art for every work that has a
// usable release MBID and none recorded yet (section 21.6). Direct-wired the
// same way the rest of internal/media/music is until stage 9.
type ArtworkJob struct {
	artworks        library.Artworks
	coverArt        CoverArtFetcher
	pictures        EmbeddedPictureReader
	roots           RootFolders
	http            *httpx.Client
	fs              fsx.FS
	imagesDir       string
	clock           clock.Clock
	logger          *slog.Logger
	coverSources    func() []string
	theAudioDBCover artworkSource
	onDone          func(context.Context)
}

// EmbeddedPictureReader copies a file's own cover art out, or answers
// mediatool.ErrNoEmbeddedPicture -- mediatool.Subprocess's own method, named
// here so no other Tool implementation has to grow it.
type EmbeddedPictureReader interface {
	EmbeddedPicture(ctx context.Context, path string) ([]byte, error)
}

// RootFolders resolves a root folder by id; root folders are settings.db
// policy (ADR-25), so a job asks rather than reading the table.
type RootFolders interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// ArtworkJobConfig is what an ArtworkJob needs. Every field is required except
// Logger, and Pictures and Roots, without which embedded art is not read.
type ArtworkJobConfig struct {
	Artworks  library.Artworks
	CoverArt  CoverArtFetcher
	Pictures  EmbeddedPictureReader
	Roots     RootFolders
	HTTP      *httpx.Client
	FS        fsx.FS
	ImagesDir string
	Clock     clock.Clock
	Logger    *slog.Logger
	// CoverSources is the configured order, read per run: settings.CoverSourceArchive and
	// settings.CoverSourceEmbedded, a source left out not used. Nil is the archive, then the files.
	CoverSources func() []string
	// TheAudioDBCover is optional: a release group's cover URL from TheAudioDB, or
	// musicbrainz.ErrNoCoverArt when it has none.
	TheAudioDBCover func(ctx context.Context, releaseGroupMBID string) (string, error)
	// OnDone is optional: told when a sweep finished, so new covers reach the tag writer at once.
	OnDone func(context.Context)
}

// NewArtworkJob builds the handler.
func NewArtworkJob(config ArtworkJobConfig) *ArtworkJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ArtworkJob{
		artworks: config.Artworks, coverArt: config.CoverArt, pictures: config.Pictures, roots: config.Roots,
		http: config.HTTP, coverSources: config.CoverSources, theAudioDBCover: config.TheAudioDBCover,
		fs: config.FS, imagesDir: config.ImagesDir, clock: config.Clock, logger: logger, onDone: config.OnDone,
	}
}

// Run processes every work it can find needing cover art, in bounded batches,
// until none remain or the context is cancelled. No persisted cursor: a
// recorded work stops being returned by NeedingArtwork, so a restart simply
// asks the same question again (EnrichJob's Run doc comment, same reasoning).
func (j *ArtworkJob) Run(ctx context.Context, job jobs.Job) error {
	if err := j.fs.MkdirAll(j.imagesDir, 0o755); err != nil {
		return fmt.Errorf("music: artwork: create %s: %w", j.imagesDir, err)
	}

	// Each source fills only the works still without a cover, so the order decides which wins.
	sources := settings.DefaultCoverSources
	if j.coverSources != nil {
		sources = j.coverSources()
	}
	for _, source := range sources {
		switch source {
		case settings.CoverSourceArchive:
			// Releases first, as the more exact image; then release groups, for wanted albums with
			// no edition and releases the archive had nothing for.
			if err := j.sweep(ctx, job.Progress, "release", j.artworks.NeedingArtwork, j.coverArt.Fetch); err != nil {
				return err
			}
			if err := j.sweep(ctx, job.Progress, "release-group", j.artworks.NeedingReleaseGroupArtwork,
				j.coverArt.FetchReleaseGroup); err != nil {
				return err
			}
		case settings.CoverSourceEmbedded:
			if err := j.sweepEmbedded(ctx, job.Progress); err != nil {
				return err
			}
		case settings.CoverSourceTheAudioDB:
			if j.theAudioDBCover == nil {
				continue
			}
			if err := j.sweep(ctx, job.Progress, "theaudiodb", j.artworks.NeedingReleaseGroupArtwork, j.theAudioDBCover); err != nil {
				return err
			}
		}
	}
	if j.onDone != nil {
		j.onDone(ctx)
	}
	return nil
}

// sweepEmbedded reads cover art out of one known file per work still without
// a cover, walked by work id for the same reason sweep is.
func (j *ArtworkJob) sweepEmbedded(ctx context.Context, progress *jobs.Progress) error {
	if j.pictures == nil || j.roots == nil {
		return nil
	}
	total, failed, skipped := 0, 0, 0
	var afterWorkID int64
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("artwork.context", string(ArtworkJobType), err)
		}

		batch, err := j.artworks.NeedingEmbeddedArtwork(ctx, string(MediaType), afterWorkID, artworkBatchSize)
		if err != nil {
			return fmt.Errorf("music: artwork: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, candidate := range batch {
			total++
			afterWorkID = candidate.WorkID
			switch err := j.embeddedOne(ctx, candidate); {
			case errors.Is(err, mediatool.ErrNoEmbeddedPicture):
				skipped++
			case err != nil:
				failed++
				j.logger.WarnContext(ctx, "embedded cover art failed",
					slog.Int64("work_id", candidate.WorkID), slog.Any("error", err))
			}
		}
		progress.Add(int64(len(batch)))
		if len(batch) < artworkBatchSize {
			break
		}
	}
	j.logger.Info("music artwork sweep finished", slog.String("kind", "embedded"),
		slog.Int("processed", total), slog.Int("failed", failed), slog.Int("skipped", skipped))
	return nil
}

func (j *ArtworkJob) embeddedOne(ctx context.Context, candidate library.EmbeddedArtworkCandidate) error {
	root, found := j.roots.RootFolder(candidate.RootFolderID)
	if !found {
		return fmt.Errorf("root folder %d is not configured", candidate.RootFolderID)
	}
	source, err := j.pictures.EmbeddedPicture(ctx, path.Join(root.Path, candidate.RelPath))
	if err != nil {
		return err
	}
	return j.storeCover(ctx, candidate.WorkID, "", source)
}

type artworkPage func(ctx context.Context, afterWorkID, pageSize int64) ([]library.ArtworkCandidate, error)

type artworkSource func(ctx context.Context, mbid string) (string, error)

// sweep walks one kind of candidate by work id. A work with no archive art
// records nothing, so re-reading from the top would return the same page
// indefinitely.
func (j *ArtworkJob) sweep(
	ctx context.Context, progress *jobs.Progress, kind string, page artworkPage, lookup artworkSource,
) error {
	total, failed, skipped := 0, 0, 0
	var afterWorkID int64
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("artwork.context", string(ArtworkJobType), err)
		}

		batch, err := page(ctx, afterWorkID, artworkBatchSize)
		if err != nil {
			return fmt.Errorf("music: artwork: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		outcomes := parallel.Do(ctx, len(batch), artworkWorkers, func(ctx context.Context, index int) error {
			return j.processOne(ctx, batch[index], lookup)
		})
		progress.Add(int64(len(batch)))
		for index, candidate := range batch {
			total++
			afterWorkID = candidate.WorkID
			switch err := outcomes[index]; {
			case errors.Is(err, musicbrainz.ErrNoCoverArt):
				// Not a failure: nothing in the archive. Asked again next sweep,
				// which is cheap and right if art is added later.
				skipped++
			case err != nil:
				failed++
				j.logger.WarnContext(ctx, "cover art fetch failed", slog.String("kind", kind),
					slog.Int64("work_id", candidate.WorkID), slog.Any("error", err))
			}
		}
		if len(batch) < artworkBatchSize {
			break
		}
	}
	j.logger.Info("music artwork sweep finished", slog.String("kind", kind),
		slog.Int("processed", total), slog.Int("failed", failed), slog.Int("skipped", skipped))
	return nil
}

func (j *ArtworkJob) processOne(ctx context.Context, candidate library.ArtworkCandidate, lookup artworkSource) error {
	imageURL, err := lookup(ctx, candidate.MBID)
	if err != nil {
		return err
	}
	source, err := j.download(ctx, imageURL)
	if err != nil {
		return err
	}
	return j.storeCover(ctx, candidate.WorkID, imageURL, source)
}

// download reads one image, bounded by maxImageBytes.
func (j *ArtworkJob) download(ctx context.Context, imageURL string) ([]byte, error) {
	response, err := j.http.Get(ctx, imageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch image: status %d", response.StatusCode)
	}
	source, err := io.ReadAll(io.LimitReader(response.Body, maxImageBytes))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	return source, nil
}

// storeCover resizes, writes and records one work's cover from its source
// bytes, however they were obtained. sourceURL is empty for art read from a file.
func (j *ArtworkJob) storeCover(ctx context.Context, workID int64, sourceURL string, source []byte) error {
	cacheKey := fmt.Sprintf("work-%d-cover", workID)
	full, err := j.writeSizes(cacheKey, source)
	if err != nil {
		return err
	}
	return j.artworks.Record(ctx, workID, sourceURL, cacheKey, full.Width, full.Height, j.clock.Now().Unix())
}

// storeImage is storeCover for a person's image.
func (j *ArtworkJob) storeImage(ctx context.Context, personID int64, sourceURL string, source []byte) error {
	cacheKey := fmt.Sprintf("person-%d-cover", personID)
	full, err := j.writeSizes(cacheKey, source)
	if err != nil {
		return err
	}
	return j.artworks.RecordForPerson(ctx, personID, sourceURL, cacheKey, full.Width, full.Height, j.clock.Now().Unix())
}

// writeSizes encodes source at every size under cacheKey and answers the full one.
func (j *ArtworkJob) writeSizes(cacheKey string, source []byte) (artwork.Image, error) {
	sizes, err := artwork.Process(source)
	if err != nil {
		return artwork.Image{}, fmt.Errorf("process image: %w", err)
	}
	if err := j.fs.MkdirAll(j.imagesDir, 0o755); err != nil {
		return artwork.Image{}, fmt.Errorf("create %s: %w", j.imagesDir, err)
	}
	for size, image := range sizes {
		destination := filepath.Join(j.imagesDir, fmt.Sprintf("%s-%s.webp", cacheKey, artworkSizeName(size)))
		if err := fsx.WriteAtomic(j.fs, destination, func(w io.Writer) error {
			_, err := w.Write(image.Bytes)
			return err
		}); err != nil {
			return artwork.Image{}, fmt.Errorf("write %s: %w", destination, err)
		}
	}
	return sizes[artwork.Full], nil
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
