package music

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/jobs"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/metadata/wikidata"
	"github.com/makearr/MediaNexus/internal/platform/artwork"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/httpx"
)

// maxPersonImageBytes mirrors ArtworkJob's own maxImageBytes -- a person's
// Wikidata photo is not expected to be any larger than a release cover, and
// a source serving far more is a reason to stop reading, not to buffer it
// all first.
const maxPersonImageBytes = 20 << 20

// Biographer is what EnrichJob needs to turn a Wikidata QID into a real
// biography (ADR-8's cross-provider bridge) -- internal/metadata/wikidata.Fetcher's
// own method, named here so this package never imports wikidata for more
// than the one type its return value needs.
type Biographer interface {
	Biography(ctx context.Context, qid string) (wikidata.Biography, error)
}

// EnrichJobType is the registered type of the background enrichment sweep.
const EnrichJobType = jobs.Type("music.enrich")

// EnrichActionKey deduplicates the sweep: it is global, not per-root, so at most
// one may be active at a time (Invariant 16) the same way a per-root scan is.
const EnrichActionKey = "music.enrich"

// CreditRoleArtist is the one credit role this pass writes -- a release
// group's own artist-credit. Role-rich credits beyond "artist" (producer,
// composer, songwriter) need MusicBrainz's relationship data, real scope of
// its own (architecture.md's §19.5, deliberately not attempted this pass).
const CreditRoleArtist = "artist"

// EnrichDefinition declares how enrichment behaves: background, low priority,
// retried on transient failure, never blocking anything a user is waiting on.
var EnrichDefinition = jobs.Definition{
	Type: EnrichJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// enrichBatchSize bounds how many placeholder editions one pass asks a provider
// about before checking in with ListPlaceholderEditions again. Small enough that
// a shutdown mid-run (ctx cancelled between batches) loses at most one batch's
// worth of otherwise-committed progress -- each batch's writes are already their
// own transaction (library.RefineEdition), so nothing partial survives either way.
const enrichBatchSize = 20

// EnrichJob refines every placeholder edition (kind='unknown') it can find into
// real metadata, and populates the parts (tracks) under it. Direct-wired the way
// the rest of internal/media/music is until stage 9.
type EnrichJob struct {
	identity  library.Identity
	people    library.People
	provider  musicbrainz.Provider
	biography Biographer
	artworks  library.Artworks
	http      *httpx.Client
	fs        fsx.FS
	imagesDir string
	clock     clock.Clock
	logger    *slog.Logger
}

// EnrichJobConfig is what an EnrichJob needs. Every field is required except
// Biography and Logger -- a nil Biography resolves people with MusicBrainz's
// own facts only, no prose biography (and, following from that, no image
// either: Wikidata's own P18 claim is read from the same lookup).
type EnrichJobConfig struct {
	Identity  library.Identity
	People    library.People
	Provider  musicbrainz.Provider
	Biography Biographer
	Artworks  library.Artworks
	HTTP      *httpx.Client
	FS        fsx.FS
	ImagesDir string
	Clock     clock.Clock
	Logger    *slog.Logger
}

// NewEnrichJob builds the handler.
func NewEnrichJob(config EnrichJobConfig) *EnrichJob {
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EnrichJob{
		identity: config.Identity, people: config.People, provider: config.Provider,
		biography: config.Biography, artworks: config.Artworks, http: config.HTTP,
		fs: config.FS, imagesDir: config.ImagesDir, clock: config.Clock, logger: logger,
	}
}

// Run processes every placeholder edition it can find, in bounded batches, until
// none remain or the context is cancelled. There is no persisted cursor: a
// refined edition's kind moves off 'unknown', so PlaceholderEditions' own result
// set shrinks as work succeeds -- a restart simply asks the same question again
// and gets whatever is left (library.PlaceholderEditions's doc comment).
func (j *EnrichJob) Run(ctx context.Context, _ jobs.Job) error {
	if j.fs != nil && j.imagesDir != "" {
		if err := j.fs.MkdirAll(j.imagesDir, 0o755); err != nil {
			return fmt.Errorf("music: enrich: create %s: %w", j.imagesDir, err)
		}
	}

	total, failed := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return jobs.Retryablef("enrich.context", string(EnrichJobType), err)
		}

		batch, err := j.identity.PlaceholderEditions(ctx, enrichBatchSize)
		if err != nil {
			return fmt.Errorf("music: enrich: %w", err)
		}
		if len(batch) == 0 {
			break
		}

		for _, edition := range batch {
			total++
			if err := j.enrichOne(ctx, edition); err != nil {
				failed++
				// One placeholder edition failing (no data at the provider, a
				// transient network error) is not a reason to stop the sweep --
				// it simply stays a placeholder and is retried next run.
				j.logger.WarnContext(ctx, "edition enrichment failed",
					slog.Int64("edition_id", edition.EditionID), slog.Any("error", err))
			}
		}
		if len(batch) < enrichBatchSize {
			break
		}
	}
	j.logger.Info("music enrichment finished", slog.Int("processed", total), slog.Int("failed", failed))
	return nil
}

func (j *EnrichJob) enrichOne(ctx context.Context, edition library.PlaceholderEdition) error {
	releaseGroupMBID, ok, err := j.identity.WorkExternalID(ctx, edition.WorkID, ProviderReleaseGroup)
	if err != nil {
		return fmt.Errorf("release group id: %w", err)
	}
	if !ok {
		// A placeholder edition always comes from newEditionLink, which links
		// the release-group id in the same transaction -- this should not
		// happen, but an edition missing its work's identity is not this job's
		// to invent, so it is skipped rather than guessed at.
		return fmt.Errorf("work %d has no %s external id", edition.WorkID, ProviderReleaseGroup)
	}

	rg, release, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, releaseGroupMBID, edition.ExternalID)
	if err != nil {
		return fmt.Errorf("musicbrainz: %w", err)
	}

	workYear, workReleaseDate, workPrimaryType, refinement, parts := refinementFor(rg, release)
	if err := j.identity.RefineEdition(ctx, j.clock.Now().Unix(),
		edition.EditionID, edition.WorkID, workYear, workReleaseDate, workPrimaryType, refinement, parts); err != nil {
		return fmt.Errorf("refine: %w", err)
	}

	// Best-effort: a credit, genre or description that fails to resolve is not
	// a reason to fail an edition that otherwise refined correctly. Logged,
	// not returned.
	j.enrichCreditsAndGenres(ctx, edition.WorkID, rg)
	j.enrichDescription(ctx, edition.WorkID, rg)
	return nil
}

// enrichCreditsAndGenres populates ADR-32's role="artist" credits and ADR-33's
// genre tags from the same release group Album already fetched -- no extra
// request for the genres (inc=genres rides along with artist-credits), one
// more request per distinct artist for their own bio facts (Provider.Artist
// caches permanently, so a prolific artist only ever costs one real lookup
// across every edition of theirs this sweep touches).
func (j *EnrichJob) enrichCreditsAndGenres(ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup) {
	for index, credit := range rg.ArtistCredit {
		personID, err := j.resolveArtist(ctx, credit)
		if err != nil {
			j.logger.WarnContext(ctx, "artist credit resolve failed",
				slog.String("mbid", credit.MBID), slog.Any("error", err))
			continue
		}
		if err := j.people.AddCredit(ctx, workID, personID, CreditRoleArtist, index); err != nil {
			j.logger.WarnContext(ctx, "add credit failed",
				slog.Int64("work_id", workID), slog.Int64("person_id", personID), slog.Any("error", err))
		}
	}

	if len(rg.Genres) == 0 {
		return
	}
	genres := make([]library.GenreLink, len(rg.Genres))
	for index, genre := range rg.Genres {
		genres[index] = library.GenreLink{Name: genre.Name}
	}
	if err := j.people.AssignGenres(ctx, workID, genres); err != nil {
		j.logger.WarnContext(ctx, "assign genres failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
}

// enrichDescription writes the work's own description from the release
// group's Wikidata QID -- a real Wikipedia extract when the album has an
// English Wikipedia article (proven live for well-known albums: Katupoken's
// "KI Pecezinu", Dipa Cogaf's "The Fura Husu of the Teke"), Wikidata's short
// description otherwise, nothing at all when there is no linked Wikidata
// entity -- MusicBrainz's release-group annotation, checked as an
// alternative source, turned out empty for every real album tested while
// building this, so it is not used.
func (j *EnrichJob) enrichDescription(ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup) {
	if j.biography == nil || rg.WikidataQID == "" {
		return
	}
	bio, err := j.biography.Biography(ctx, rg.WikidataQID)
	if err != nil {
		j.logger.WarnContext(ctx, "work description lookup failed",
			slog.String("qid", rg.WikidataQID), slog.Any("error", err))
		return
	}
	description := bio.Extract
	if description == "" {
		description = bio.Description
	}
	if description == "" {
		return
	}
	if err := j.identity.SetWorkDescription(ctx, j.clock.Now().Unix(), workID, description); err != nil {
		j.logger.WarnContext(ctx, "set work description failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
}

// resolveArtist finds or creates the person behind one release-group credit.
// A failed Artist lookup still resolves a person from the name and id the
// artist-credit itself already carries -- thinner than a full bio, but a
// credit worth having is worth having even without one.
func (j *EnrichJob) resolveArtist(ctx context.Context, credit musicbrainz.ArtistCredit) (int64, error) {
	if credit.MBID == "" {
		return 0, fmt.Errorf("artist credit %q has no id", credit.Name)
	}

	artist, err := j.provider.Artist(ctx, musicbrainz.PriorityEnrich, credit.MBID)
	if err != nil {
		j.logger.WarnContext(ctx, "artist detail lookup failed",
			slog.String("mbid", credit.MBID), slog.Any("error", err))
		artist = musicbrainz.Artist{MBID: credit.MBID, Name: credit.Name, SortName: credit.Name}
	}

	sortName := artist.SortName
	if sortName == "" {
		sortName = artist.Name
	}
	bio := j.wikidataBiographyFor(ctx, artist)
	personID, err := j.people.ResolvePerson(ctx, j.clock.Now(), library.PersonLink{
		Provider: ProviderArtist, ExternalID: artist.MBID,
		Person: library.PersonIdentity{
			Name: artist.Name, SortName: sortName, Disambiguation: artist.Disambiguation,
			Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
			Born: artist.Born, Died: artist.Died, Biography: biographyText(bio),
		},
	})
	if err != nil {
		return 0, err
	}
	// Best-effort, same as the biography text above: a person is still worth
	// having with no image at all. Album covers falling back to this image
	// (todo 4.13's own follow-up, asked for 2026-09-11) is what makes fetching
	// it here, rather than only when someone opens the Artists screen, worth
	// doing proactively.
	j.fetchPersonImage(ctx, personID, bio.ImageURL)
	return personID, nil
}

// wikidataBiographyFor is biographyFor's own lookup, factored out so its one
// Wikidata response feeds both the biography text (biographyText) and the
// image fetch (fetchPersonImage) rather than being fetched twice. Returns
// the zero Biography (empty ImageURL and text) when j.biography is nil, the
// artist has no Wikidata QID, or the lookup itself failed -- never fails
// resolveArtist over this.
func (j *EnrichJob) wikidataBiographyFor(ctx context.Context, artist musicbrainz.Artist) wikidata.Biography {
	if j.biography == nil || artist.WikidataQID == "" {
		return wikidata.Biography{}
	}
	bio, err := j.biography.Biography(ctx, artist.WikidataQID)
	if err != nil {
		j.logger.WarnContext(ctx, "biography lookup failed",
			slog.String("qid", artist.WikidataQID), slog.Any("error", err))
		return wikidata.Biography{}
	}
	return bio
}

// biographyText picks the Wikipedia extract when there is one, Wikidata's
// own short description otherwise -- biographyFor's own preference, kept as
// a pure function now that the fetch itself lives in wikidataBiographyFor.
func biographyText(bio wikidata.Biography) string {
	if bio.Extract != "" {
		return bio.Extract
	}
	return bio.Description
}

// fetchPersonImage downloads, processes and records personID's own Wikidata
// photo -- ArtworkJob.processOne's own shape, duplicated rather than shared
// (two callers, ADR-23's own "three similar things: extract" threshold not
// met yet, and the two differ in subject type and cache key). Never fails
// the credit it was resolved for: a person is still worth having with no
// image, the same tolerance every other best-effort step in this job keeps.
func (j *EnrichJob) fetchPersonImage(ctx context.Context, personID int64, imageURL string) {
	if imageURL == "" || j.artworks == (library.Artworks{}) || j.http == nil || j.fs == nil {
		return
	}
	existing, err := j.artworks.GetForPerson(ctx, personID)
	if err != nil {
		j.logger.WarnContext(ctx, "person artwork lookup failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return
	}
	if existing.Found {
		return
	}

	response, err := j.http.Get(ctx, imageURL)
	if err != nil {
		j.logger.WarnContext(ctx, "person image fetch failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		j.logger.WarnContext(ctx, "person image fetch failed",
			slog.Int64("person_id", personID), slog.Int("status", response.StatusCode))
		return
	}
	source, err := io.ReadAll(io.LimitReader(response.Body, maxPersonImageBytes))
	if err != nil {
		j.logger.WarnContext(ctx, "person image read failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return
	}

	sizes, err := artwork.Process(source)
	if err != nil {
		j.logger.WarnContext(ctx, "person image process failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return
	}

	cacheKey := fmt.Sprintf("person-%d-cover", personID)
	for size, image := range sizes {
		destination := filepath.Join(j.imagesDir, fmt.Sprintf("%s-%s.webp", cacheKey, artworkSizeName(size)))
		if err := fsx.WriteAtomic(j.fs, destination, func(w io.Writer) error {
			_, err := w.Write(image.Bytes)
			return err
		}); err != nil {
			j.logger.WarnContext(ctx, "person image write failed",
				slog.Int64("person_id", personID), slog.Any("error", err))
			return
		}
	}

	full := sizes[artwork.Full]
	if err := j.artworks.RecordForPerson(
		ctx, personID, imageURL, cacheKey, full.Width, full.Height, j.clock.Now().Unix(),
	); err != nil {
		j.logger.WarnContext(ctx, "person artwork record failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
	}
}

// EnqueueEnrichment submits one sweep, deduplicated by EnrichActionKey.
func EnqueueEnrichment(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: EnrichJobType, ActionKey: EnrichActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
