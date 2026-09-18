package music

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
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

// personBackfillBatchSize and personBackfillBudget bound the artist image and
// biography sweep. Each person costs a MusicBrainz lookup plus a Wikidata one,
// and MusicBrainz is 1 req/s shared with everything else (ADR-13), so the
// sweep takes a slice per run rather than the whole backlog at once.
const (
	personBackfillBatchSize = 20
	personBackfillBudget    = 60
)

// editionlessBatchSize and editionlessBudget bound the pass over works the
// text search identified: one release-group lookup each, on the same shared
// MusicBrainz budget.
const (
	editionlessBatchSize = 20
	editionlessBudget    = 60
)

// EnrichJob refines every placeholder edition (kind='unknown') it can find into
// real metadata, and populates the parts (tracks) under it. Direct-wired the way
// the rest of internal/media/music is until stage 9.
type EnrichJob struct {
	identity  library.Identity
	people    library.People
	provider  musicbrainz.Provider
	biography Biographer
	artworks  library.Artworks
	realigner Realigner
	tags      TagReader
	roots     RootFolders
	http      *httpx.Client
	fs        fsx.FS
	imagesDir string
	clock     clock.Clock
	logger    *slog.Logger
}

// TagReader reads the tags a set of files carry -- mediatool.Tool's own
// method, named here so enrichment asks for nothing more.
type TagReader interface {
	ReadTags(ctx context.Context, paths []string) ([]mediatool.Probe, error)
}

// maxEditionChoiceFiles bounds how many of an album's files are read to choose
// its edition; a larger set is judged on this many.
const maxEditionChoiceFiles = 200

// Realigner matches a work's files to its tracks again, once enrichment has
// given it a tracklist. A scan matches files only against tracks that exist,
// and on a fresh library the tracklist arrives afterwards, so without this
// every album on disk stayed wanted with no files present (found 2026-09-17).
type Realigner interface {
	RealignWorks(ctx context.Context, workIDs []int64) error
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
	// Realigner is optional: without one, refined albums wait for the next
	// scan that happens to revisit their directory.
	Realigner Realigner
	// Tags and Roots are optional: without both, an album identified only at
	// release-group level gets no edition chosen and so no tracklist.
	Tags      TagReader
	Roots     RootFolders
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
		biography: config.Biography, artworks: config.Artworks, realigner: config.Realigner,
		tags: config.Tags, roots: config.Roots, http: config.HTTP,
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
	var refinedWorkIDs []int64
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
			if err := j.enrichOne(ctx, edition); err == nil {
				refinedWorkIDs = append(refinedWorkIDs, edition.WorkID)
			} else {
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

	refinedWorkIDs = append(refinedWorkIDs, j.enrichEditionless(ctx)...)

	if j.realigner != nil && len(refinedWorkIDs) > 0 {
		if err := j.realigner.RealignWorks(ctx, refinedWorkIDs); err != nil {
			j.logger.WarnContext(ctx, "realign refined works failed",
				slog.Int("works", len(refinedWorkIDs)), slog.Any("error", err))
		}
	}

	j.backfillPeople(ctx)
	return nil
}

// enrichEditionless gives works identified at release-group level -- the text
// search's output, which has no edition -- their artist, genres, description,
// year and type. The placeholder-edition pass above never reached them, so
// they sat under "Unknown artist" (found 2026-09-17). No tracklist: that needs
// an edition, which choosing is not this pass's call. Walked by work id within
// a run and bounded by editionlessBudget.
func (j *EnrichJob) enrichEditionless(ctx context.Context) (withEdition []int64) {
	enriched, failed := 0, 0
	var afterWorkID int64
	for enriched+failed < editionlessBudget {
		if ctx.Err() != nil {
			return withEdition
		}
		batch, err := j.identity.EditionlessWorks(ctx, afterWorkID, editionlessBatchSize)
		if err != nil {
			j.logger.WarnContext(ctx, "editionless works failed", slog.Any("error", err))
			return withEdition
		}
		if len(batch) == 0 {
			break
		}
		for _, work := range batch {
			if enriched+failed >= editionlessBudget {
				break
			}
			afterWorkID = work.WorkID
			rg, _, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, work.ReleaseGroupMBID, "")
			if err != nil {
				failed++
				j.logger.WarnContext(ctx, "editionless work enrichment failed",
					slog.Int64("work_id", work.WorkID), slog.Any("error", err))
				continue
			}
			workYear, workReleaseDate, workPrimaryType, _, _ := refinementFor(rg, musicbrainz.Release{})
			if err := j.identity.RefineWork(ctx, j.clock.Now().Unix(), work.WorkID,
				workYear, workReleaseDate, workPrimaryType); err != nil {
				failed++
				j.logger.WarnContext(ctx, "editionless work refine failed",
					slog.Int64("work_id", work.WorkID), slog.Any("error", err))
				continue
			}
			j.enrichCreditsAndGenres(ctx, work.WorkID, rg)
			j.enrichDescription(ctx, work.WorkID, rg)
			enriched++
			if j.chooseEdition(ctx, work, rg) {
				withEdition = append(withEdition, work.WorkID)
			}
		}
		if len(batch) < editionlessBatchSize {
			break
		}
	}
	if enriched+failed > 0 {
		j.logger.Info("editionless enrichment finished", slog.Int("enriched", enriched), slog.Int("failed", failed),
			slog.Int("editions_chosen", len(withEdition)))
	}
	return withEdition
}

// chooseEdition gives a work identified only at release-group level the
// edition its files fit best (chooseRelease, approved 2026-09-17), then refines
// it like any placeholder edition, which builds the tracklist. True when an
// edition was chosen and refined, so the work's files can be matched to it.
func (j *EnrichJob) chooseEdition(ctx context.Context, work library.EditionlessWork, group musicbrainz.ReleaseGroup) bool {
	if j.tags == nil || j.roots == nil {
		return false
	}
	locations, err := j.identity.WorkFileLocations(ctx, []int64{work.WorkID})
	if err != nil || len(locations) == 0 {
		return false
	}
	paths := make([]string, 0, min(len(locations), maxEditionChoiceFiles))
	relPaths := make([]string, 0, cap(paths))
	for _, location := range locations[:min(len(locations), maxEditionChoiceFiles)] {
		if root, found := j.roots.RootFolder(location.RootFolderID); found {
			paths = append(paths, path.Join(root.Path, location.RelPath))
			relPaths = append(relPaths, location.RelPath)
		}
	}
	probes, err := j.tags.ReadTags(ctx, paths)
	if err != nil {
		j.logger.WarnContext(ctx, "edition choice tag read failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	locals := make([]LocalTrack, 0, len(probes))
	for index, probe := range probes {
		if probe.Err == nil {
			locals = append(locals, ReadLocalTrack(probe.Tags, probe.Measured, relPaths[index]))
		}
	}

	releases, err := j.provider.Releases(ctx, musicbrainz.PriorityEnrich, work.ReleaseGroupMBID)
	if err != nil {
		j.logger.WarnContext(ctx, "edition choice release listing failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	release, ok := chooseRelease(group, releases, locals)
	if !ok {
		return false
	}

	resolutions, err := j.identity.ResolveWorksDetailed(ctx, j.clock.Now(), []library.WorkLink{{
		Provider: ProviderReleaseGroup, ExternalID: work.ReleaseGroupMBID, Confidence: siblingConfidence,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: release.Title, SortTitle: SortTitle(release.Title)},
		Edition: &library.EditionLink{
			Provider: ProviderRelease, ExternalID: release.MBID, Confidence: siblingConfidence,
			Edition: library.EditionIdentity{Kind: "unknown", Label: release.MBID},
		},
	}})
	if err != nil || len(resolutions) != 1 || resolutions[0].EditionID == nil {
		j.logger.WarnContext(ctx, "edition choice resolve failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	if err := j.enrichOne(ctx, library.PlaceholderEdition{
		EditionID: *resolutions[0].EditionID, WorkID: work.WorkID, ExternalID: release.MBID,
	}); err != nil {
		j.logger.WarnContext(ctx, "chosen edition refine failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	return true
}

// backfillPeople fills in the image and biography of people resolved before
// those had a source. resolveArtist only ever fetches them the first time a
// person is created, and findOrCreatePerson returns an existing person
// untouched, so without this pass an artist already in the library never
// gained either -- which is why no artist picture had ever appeared
// (found 2026-09-13). Bounded per run by personBackfillBudget.
func (j *EnrichJob) backfillPeople(ctx context.Context) {
	// Without a Wikidata source every artist would look imageless and be
	// recorded so, permanently.
	if j.artworks == (library.Artworks{}) || j.biography == nil {
		return
	}

	filled, absent, failed := 0, 0, 0
	var afterPersonID int64
	for filled+absent+failed < personBackfillBudget {
		if ctx.Err() != nil {
			return
		}

		batch, err := j.artworks.PeopleNeedingArtwork(ctx, afterPersonID, personBackfillBatchSize)
		if err != nil {
			j.logger.WarnContext(ctx, "people needing artwork failed", slog.Any("error", err))
			return
		}
		if len(batch) == 0 {
			break
		}

		for _, candidate := range batch {
			if filled+absent+failed >= personBackfillBudget {
				break
			}
			afterPersonID = candidate.PersonID
			switch outcome := j.backfillOne(ctx, candidate); outcome {
			case backfillFilled:
				filled++
			case backfillAbsent:
				absent++
			default:
				failed++
			}
		}
		if len(batch) < personBackfillBatchSize {
			break
		}
	}

	if filled+absent+failed > 0 {
		j.logger.Info("artist backfill finished",
			slog.Int("filled", filled), slog.Int("no_image", absent), slog.Int("failed", failed))
	}
}

type backfillOutcome int

const (
	backfillFailed backfillOutcome = iota
	backfillFilled
	backfillAbsent
)

// backfillOne resolves one person's provider detail. A definitive "this
// artist has no Wikidata image" is recorded so the sweep stops asking --
// without it the same lowest-id people come back every run and the rest are
// never reached. A transient failure records nothing and is retried.
func (j *EnrichJob) backfillOne(ctx context.Context, candidate library.PersonArtworkCandidate) backfillOutcome {
	artist, err := j.provider.Artist(ctx, musicbrainz.PriorityEnrich, candidate.ArtistMBID)
	if err != nil {
		j.logger.WarnContext(ctx, "artist backfill lookup failed",
			slog.Int64("person_id", candidate.PersonID), slog.String("mbid", candidate.ArtistMBID),
			slog.Any("error", err))
		return backfillFailed
	}

	bio, err := j.wikidataLookup(ctx, artist)
	if err != nil {
		// A failed lookup says nothing about whether the artist has a photo.
		j.logger.WarnContext(ctx, "artist backfill wikidata lookup failed",
			slog.Int64("person_id", candidate.PersonID), slog.String("qid", artist.WikidataQID),
			slog.Any("error", err))
		return backfillFailed
	}
	if err := j.people.BackfillDetails(ctx, candidate.PersonID, library.PersonIdentity{
		Name: artist.Name, SortName: artist.SortName, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
		Born: artist.Born, Died: artist.Died, Biography: biographyText(bio),
	}); err != nil {
		j.logger.WarnContext(ctx, "artist backfill details failed",
			slog.Int64("person_id", candidate.PersonID), slog.Any("error", err))
	}

	outcome := imageUnusable
	if bio.ImageURL != "" {
		outcome = j.fetchPersonImage(ctx, candidate.PersonID, bio.ImageURL)
	}
	switch outcome {
	case imageRecorded:
		return backfillFilled
	case imageUnusable:
		if err := j.artworks.RecordPersonArtworkAbsent(ctx, candidate.PersonID, j.clock.Now().Unix()); err != nil {
			j.logger.WarnContext(ctx, "record person artwork absent failed",
				slog.Int64("person_id", candidate.PersonID), slog.Any("error", err))
			return backfillFailed
		}
		return backfillAbsent
	default:
		return backfillFailed
	}
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
	lookedUp := err == nil
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
	identity := library.PersonIdentity{
		Name: artist.Name, SortName: sortName, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
		Born: artist.Born, Died: artist.Died, Biography: biographyText(bio),
	}
	personID, err := j.people.ResolvePerson(ctx, j.clock.Now(), library.PersonLink{
		Provider: ProviderArtist, ExternalID: artist.MBID, Person: identity,
	})
	if err != nil {
		return 0, err
	}
	// An existing person is returned untouched, so one created from a failed
	// lookup kept no biography even after a later lookup succeeded -- and once
	// that later lookup recorded a photo, the backfill never came back for it.
	if lookedUp {
		if err := j.people.BackfillDetails(ctx, personID, identity); err != nil {
			j.logger.WarnContext(ctx, "artist details backfill failed",
				slog.Int64("person_id", personID), slog.Any("error", err))
		}
	}
	// Best-effort, same as the biography text above: a person is still worth
	// having with no image at all. Album covers falling back to this image
	// (todo 4.13's own follow-up, asked for 2026-09-11) is what makes fetching
	// it here, rather than only when someone opens the Artists screen, worth
	// doing proactively.
	_ = j.fetchPersonImage(ctx, personID, bio.ImageURL)
	return personID, nil
}

// wikidataBiographyFor is biographyFor's own lookup, factored out so its one
// Wikidata response feeds both the biography text (biographyText) and the
// image fetch (fetchPersonImage) rather than being fetched twice. Returns
// the zero Biography (empty ImageURL and text) when j.biography is nil, the
// artist has no Wikidata QID, or the lookup itself failed -- never fails
// resolveArtist over this.
func (j *EnrichJob) wikidataBiographyFor(ctx context.Context, artist musicbrainz.Artist) wikidata.Biography {
	bio, err := j.wikidataLookup(ctx, artist)
	if err != nil {
		j.logger.WarnContext(ctx, "biography lookup failed",
			slog.String("qid", artist.WikidataQID), slog.Any("error", err))
		return wikidata.Biography{}
	}
	return bio
}

// wikidataLookup is wikidataBiographyFor without swallowing the error, for a
// caller that must not mistake a failed lookup for an artist with no image.
func (j *EnrichJob) wikidataLookup(ctx context.Context, artist musicbrainz.Artist) (wikidata.Biography, error) {
	if j.biography == nil || artist.WikidataQID == "" {
		return wikidata.Biography{}, nil
	}
	return j.biography.Biography(ctx, artist.WikidataQID)
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
func (j *EnrichJob) fetchPersonImage(ctx context.Context, personID int64, imageURL string) imageOutcome {
	if imageURL == "" || j.artworks == (library.Artworks{}) || j.http == nil || j.fs == nil {
		return imageFailed
	}
	existing, err := j.artworks.GetForPerson(ctx, personID)
	if err != nil {
		j.logger.WarnContext(ctx, "person artwork lookup failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageFailed
	}
	if existing.Found {
		return imageRecorded
	}

	response, err := j.http.Get(ctx, imageURL)
	if err != nil {
		j.logger.WarnContext(ctx, "person image fetch failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageFailed
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		j.logger.WarnContext(ctx, "person image fetch failed",
			slog.Int64("person_id", personID), slog.Int("status", response.StatusCode))
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			return imageUnusable
		}
		return imageFailed
	}
	source, err := io.ReadAll(io.LimitReader(response.Body, maxPersonImageBytes))
	if err != nil {
		j.logger.WarnContext(ctx, "person image read failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageFailed
	}

	// The same bytes will fail to decode next run too, so this is final.
	sizes, err := artwork.Process(source)
	if err != nil {
		j.logger.WarnContext(ctx, "person image process failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageUnusable
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
			return imageFailed
		}
	}

	full := sizes[artwork.Full]
	if err := j.artworks.RecordForPerson(
		ctx, personID, imageURL, cacheKey, full.Width, full.Height, j.clock.Now().Unix(),
	); err != nil {
		j.logger.WarnContext(ctx, "person artwork record failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageFailed
	}
	return imageRecorded
}

// imageOutcome separates a person image that will never be usable from one
// that failed for a reason worth retrying.
type imageOutcome int

const (
	imageFailed imageOutcome = iota
	imageRecorded
	imageUnusable
)

// EnqueueEnrichment submits one sweep, deduplicated by EnrichActionKey.
func EnqueueEnrichment(ctx context.Context, queue *jobs.Queue) (jobs.EnqueueResult, error) {
	return queue.Enqueue(ctx, jobs.Request{
		Type: EnrichJobType, ActionKey: EnrichActionKey, Priority: jobs.PriorityLow,
		Payload: []byte("{}"),
	})
}
