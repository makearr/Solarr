package music

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
	"github.com/makearr/Solarr/internal/platform/artwork"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/platform/parallel"
)

// maxPersonImageBytes mirrors ArtworkJob's limit; a source serving far more is a reason to stop reading, not to buffer.
const maxPersonImageBytes = 20 << 20

// Biographer turns a Wikidata QID into a biography (ADR-8's bridge); named here so this package imports wikidata for one type only.
type Biographer interface {
	Biography(ctx context.Context, qid string) (wikidata.Biography, error)
	// Biographies answers many QIDs at once; a QID it has nothing for is absent.
	Biographies(ctx context.Context, qids []string) (map[string]wikidata.Biography, error)
	// GenresOf answers each item's genres, lower case; an item without any is absent.
	GenresOf(ctx context.Context, qids []string) (map[string][]string, error)
}

// ArtistArt is a second source of an artist's photo and biography, by MusicBrainz artist id
// (TheAudioDB, 2026-09-25): asked only for what Wikidata left empty.
type ArtistArt func(ctx context.Context, artistMBID string) (photoURL, biography string, err error)

// EnrichJobType is the registered type of the background enrichment sweep.
const EnrichJobType = jobs.Type("music.enrich")

// EnrichActionKey deduplicates the sweep: it is global, not per-root, so at most
// one may be active at a time (Invariant 16) the same way a per-root scan is.
const EnrichActionKey = "music.enrich"

// CreditRoleArtist is the one role written; producer, composer and the like need MusicBrainz relationships (section 19.5), not attempted.
const CreditRoleArtist = "artist"

// EnrichDefinition declares how enrichment behaves: background, low priority,
// retried on transient failure, never blocking anything a user is waiting on.
var EnrichDefinition = jobs.Definition{
	Name: "Enriching metadata",
	Type: EnrichJobType, Pool: jobs.PoolMetadata, Mutating: true, MaxAttempts: 3,
}

// enrichBatchSize bounds editions per pass so a shutdown loses at most one batch; each batch is already its own transaction.
const enrichBatchSize = 20

// enrichLookupWorkers overlaps a batch's album lookups: BrainzMash answers in ~1 s but allows
// 10 req/s, so one at a time left enrichment at a tenth of its budget (measured 2026-09-28).
const enrichLookupWorkers = 8

// personBackfillBatchSize is one page of the artist image and biography sweep; the sweep's
// per-run budget is a setting (EnrichJobConfig.PeopleBackfillPerRun).
const personBackfillBatchSize = 20

// personArtworkAbsentTTL is how long "this artist has no photo" holds before the sweep asks again.
const personArtworkAbsentTTL = 90 * 24 * time.Hour

// defaultPeopleBackfillPerRun is the budget when none is configured.
const defaultPeopleBackfillPerRun = 200

// defaultCheckpointBatches is how often a run hands its progress on: realigned files, fresh counts
// and a slice of artist backfill. On a fresh library a run lasts hours; all three used to wait for its end.
const defaultCheckpointBatches = 10

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
	identity        library.Identity
	people          library.People
	organisations   *library.Organisations
	provider        musicbrainz.Provider
	biography       Biographer
	artworks        library.Artworks
	realigner       Realigner
	libraries       Libraries
	desired         *library.Desired
	onRefined       func(context.Context)
	artistArt       ArtistArt
	tags            TagReader
	roots           RootFolders
	http            *httpx.Client
	fs              fsx.FS
	imagesDir       string
	clock           clock.Clock
	logger          *slog.Logger
	wikidataEnabled func() bool
	albumRatings    AlbumRatings
	backfillPerRun  func() int
	checkpointEvery int
	// editionlessAfter carries enrichEditionless' place between runs, so works that never gain an edition cannot hold every later one back.
	editionlessAfter atomic.Int64
}

// AlbumRating is one provider's rating of an album on the library's 0..10, with the people behind it.
type AlbumRating struct {
	Provider string
	Score    float64
	Votes    int64
}

// AlbumRatings answers the ratings the outside providers hold of an album, found by its release group and its credited artist and title.
type AlbumRatings func(ctx context.Context, releaseGroupMBID, artist, title string) []AlbumRating

// rateFromOthers records the popularity ListenBrainz and Deezer hold; a provider that knows nothing leaves no rating.
func (j *EnrichJob) rateFromOthers(ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup) {
	if j.albumRatings == nil {
		return
	}
	artist := ""
	if len(rg.ArtistCredit) > 0 {
		artist = cmp.Or(rg.ArtistCredit[0].ArtistName, rg.ArtistCredit[0].Name)
	}
	for _, rating := range j.albumRatings(ctx, rg.MBID, artist, rg.Title) {
		if err := j.people.SetRating(ctx, workID, rating.Provider, rating.Score, rating.Votes, j.clock.Now().Unix()); err != nil {
			j.logger.WarnContext(ctx, "set rating failed", slog.Int64("work_id", workID), slog.String("provider", rating.Provider), slog.Any("error", err))
		}
	}
}

// TagReader reads the tags a set of files carry -- mediatool.Tool's own
// method, named here so enrichment asks for nothing more.
type TagReader interface {
	ReadTags(ctx context.Context, paths []string) ([]mediatool.Probe, error)
}

// maxEditionChoiceFiles bounds how many of an album's files are read to choose
// its edition; a larger set is judged on this many.
const maxEditionChoiceFiles = 200

// Realigner re-matches a work's files to tracks once enrichment supplies a tracklist; without it albums on a fresh library stayed wanted with files present.
type Realigner interface {
	RealignWorks(ctx context.Context, workIDs []int64, manual bool) error
}

// EnrichJobConfig is required except Biography and Logger; a nil Biography means MusicBrainz facts only, no prose and no image.
type EnrichJobConfig struct {
	Identity library.Identity
	People   library.People
	// Organisations is optional: without it albums keep no label.
	Organisations *library.Organisations
	Provider      musicbrainz.Provider
	Biography     Biographer
	// WikidataEnabled is the configured switch, read per artist; nil is on.
	WikidataEnabled func() bool
	// PeopleBackfillPerRun is the configured budget, read per run; nil is 200.
	PeopleBackfillPerRun func() int
	// AlbumRatings asks the providers beyond MusicBrainz about an album's popularity; nil asks none.
	AlbumRatings AlbumRatings
	// ArtistArt is optional: without it an artist Wikidata has no photo for keeps none.
	ArtistArt ArtistArt
	Artworks  library.Artworks
	// Realigner is optional: without one, refined albums wait for the next
	// scan that happens to revisit their directory.
	Realigner Realigner
	// Libraries is optional: without it every lookup takes the default provider order.
	Libraries Libraries
	// Desired is optional: with it, a refined album's files are aligned to its new tracklist at once
	// from what scan and import claimed for them.
	Desired *library.Desired
	// OnRefined is optional: told when a run refined any album, so its new tracks are counted at once.
	OnRefined func(context.Context)
	// CheckpointBatches is how many batches pass between checkpoints; zero is 10.
	CheckpointBatches int
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
		identity: config.Identity, people: config.People, organisations: config.Organisations, provider: config.Provider,
		biography: config.Biography, wikidataEnabled: config.WikidataEnabled, albumRatings: config.AlbumRatings,
		backfillPerRun: config.PeopleBackfillPerRun, checkpointEvery: config.CheckpointBatches,
		artworks: config.Artworks, realigner: config.Realigner, libraries: config.Libraries, desired: config.Desired, onRefined: config.OnRefined,
		artistArt: config.ArtistArt,
		tags:      config.Tags, roots: config.Roots, http: config.HTTP,
		fs: config.FS, imagesDir: config.ImagesDir, clock: config.Clock, logger: logger,
	}
}

// Run processes placeholder editions in bounded batches until none remain or the context ends; no cursor, since refined editions leave the result set.
func (j *EnrichJob) Run(ctx context.Context, job jobs.Job) error {
	var payload EnrichPayload
	if len(job.Payload) > 0 {
		// An unreadable payload is an ordinary scheduled run, as every run was before it had one.
		_ = json.Unmarshal(job.Payload, &payload)
	}
	if j.fs != nil && j.imagesDir != "" {
		if err := j.fs.MkdirAll(j.imagesDir, 0o755); err != nil {
			return fmt.Errorf("music: enrich: create %s: %w", j.imagesDir, err)
		}
	}

	checkpointEvery := j.checkpointEvery
	if checkpointEvery <= 0 {
		checkpointEvery = defaultCheckpointBatches
	}
	backfill := backfillProgress{budget: defaultPeopleBackfillPerRun}
	if j.backfillPerRun != nil {
		backfill.budget = j.backfillPerRun()
	}
	artists := newArtistMemo()
	if waiting, err := j.identity.CountPlaceholderEditions(ctx); err == nil {
		job.Progress.Set(0, waiting)
	}
	total, failed, batches := 0, 0, 0
	var refinedWorkIDs []int64
	var afterEditionID int64
	// sweep refines every placeholder past the cursor until none is left: a short batch is not the end while a scan is
	// still adding them (seen 2026-10-13: a run that began beside a scan stopped after its first six of 189).
	sweep := func() error {
		for {
			if err := ctx.Err(); err != nil {
				return jobs.Retryablef("enrich.context", string(EnrichJobType), err)
			}

			// The cursor, not the shrinking set: 20 editions failing for good once looped forever (2026-09-26).
			batch, err := j.identity.PlaceholderEditions(ctx, afterEditionID, enrichBatchSize)
			if err != nil {
				return fmt.Errorf("music: enrich: %w", err)
			}
			if len(batch) == 0 {
				break
			}

			albums := make([]albumKey, len(batch))
			for index, edition := range batch {
				albums[index] = j.placeholderAlbum(ctx, edition)
			}
			answers := j.lookupAlbums(ctx, albums)
			for index, edition := range batch {
				afterEditionID = edition.EditionID
				total++
				job.Progress.Add(1)
				workCtx := musicbrainz.ForLibrary(ctx, albums[index].library)
				err := j.enrichOne(workCtx, edition, answers[index], artists)
				if err == nil {
					j.followMergedIDs(ctx, edition, albums[index], answers[index])
				}
				if errors.Is(err, musicbrainz.ErrNotFound) && j.replaceRetiredRelease(workCtx, edition, albums[index], artists) {
					err = nil
				}
				if err == nil {
					refinedWorkIDs = append(refinedWorkIDs, edition.WorkID)
					j.alignClaimed(ctx, edition.WorkID)
				} else {
					failed++
					// One placeholder edition failing (no data at the provider, a
					// transient network error) is not a reason to stop the sweep --
					// it stays a placeholder, the cursor moves past it, and the next run retries it.
					j.logger.WarnContext(ctx, "edition enrichment failed",
						slog.Int64("edition_id", edition.EditionID), slog.Any("error", err))
				}
			}
			if batches++; batches%checkpointEvery == 0 {
				j.handOn(ctx, refinedWorkIDs, payload.Manual)
				refinedWorkIDs = nil
				j.backfillPeople(ctx, &backfill, personBackfillBatchSize)
			}
		}
		return nil
	}
	if err := sweep(); err != nil {
		return err
	}
	j.logger.Info("music enrichment finished", slog.Int("processed", total), slog.Int("failed", failed))

	refinedWorkIDs = append(refinedWorkIDs, j.enrichEditionless(ctx, artists)...)
	j.handOn(ctx, refinedWorkIDs, payload.Manual)
	j.backfillPeople(ctx, &backfill, backfill.budget)
	// A request made while this run was busy was dropped as its duplicate: what it asked for is looked at here.
	refinedWorkIDs = nil
	if err := sweep(); err != nil {
		return err
	}
	j.handOn(ctx, refinedWorkIDs, payload.Manual)
	// Labels after the albums: a release refined in this run is cached, so asking costs nothing.
	j.backfillLabels(ctx, labelBackfillPerRun)
	return nil
}

// handOn gives refined works to the realigner, so their files meet the new tracklist, and has them counted.
func (j *EnrichJob) handOn(ctx context.Context, refinedWorkIDs []int64, manual bool) {
	if len(refinedWorkIDs) == 0 {
		return
	}
	if j.realigner != nil {
		if err := j.realigner.RealignWorks(ctx, refinedWorkIDs, manual); err != nil {
			j.logger.WarnContext(ctx, "realign refined works failed",
				slog.Int("works", len(refinedWorkIDs)), slog.Any("error", err))
		}
	}
	if j.onRefined != nil {
		j.onRefined(ctx)
	}
}

// enrichEditionless gives release-group-level works (no edition) their artist, genres, description, year and type; no tracklist, since choosing an edition is not this pass's call; bounded by editionlessBudget.
func (j *EnrichJob) enrichEditionless(ctx context.Context, artists artistMemo) (withEdition []int64) {
	enriched, failed, retired := 0, 0, 0
	afterWorkID := j.editionlessAfter.Load()
	defer func() { j.editionlessAfter.Store(afterWorkID) }()
	for enriched+failed+retired < editionlessBudget {
		if ctx.Err() != nil {
			return withEdition
		}
		batch, err := j.identity.EditionlessWorks(ctx, afterWorkID, editionlessBatchSize)
		if err != nil {
			j.logger.WarnContext(ctx, "editionless works failed", slog.Any("error", err))
			return withEdition
		}
		if len(batch) == 0 {
			afterWorkID = 0
			break
		}
		batch = batch[:min(len(batch), editionlessBudget-enriched-failed-retired)]
		albums := make([]albumKey, len(batch))
		for index, work := range batch {
			albums[index] = albumKey{releaseGroup: work.ReleaseGroupMBID, library: j.workLibrary(ctx, work.WorkID)}
		}
		answers := j.lookupAlbums(ctx, albums)
		for index, work := range batch {
			afterWorkID = work.WorkID
			rg, err := answers[index].rg, answers[index].err
			if errors.Is(err, musicbrainz.ErrNotFound) {
				// Asked again every run it would fail every run; its files are identified afresh instead.
				retired++
				j.returnRetiredToReview(ctx, work.WorkID, work.ReleaseGroupMBID)
				continue
			}
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
			workCtx := musicbrainz.ForLibrary(ctx, albums[index].library)
			j.retitle(ctx, work.WorkID, rg)
			j.enrichCreditsAndGenres(workCtx, work.WorkID, rg, artists)
			j.enrichDescription(ctx, work.WorkID, answers[index])
			enriched++
			if j.chooseEdition(workCtx, work, rg, artists) {
				withEdition = append(withEdition, work.WorkID)
				j.alignClaimed(ctx, work.WorkID)
			}
		}
		if len(batch) < editionlessBatchSize {
			afterWorkID = 0
			break
		}
	}
	if enriched+failed+retired > 0 {
		j.logger.Info("editionless enrichment finished", slog.Int("enriched", enriched), slog.Int("failed", failed),
			slog.Int("retired", retired), slog.Int("editions_chosen", len(withEdition)))
	}
	return withEdition
}

// retitle names the work after its release group; best-effort, like the credits.
func (j *EnrichJob) retitle(ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup) {
	if rg.Title == "" {
		return
	}
	if err := j.identity.RetitleWork(ctx, j.clock.Now().Unix(), workID, rg.Title, library.SortTitle(rg.Title)); err != nil {
		j.logger.WarnContext(ctx, "retitle work failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
}

// alignClaimed attaches a refined work's files to its new tracklist; best-effort, like the credits.
func (j *EnrichJob) alignClaimed(ctx context.Context, workID int64) {
	if j.desired == nil {
		return
	}
	if err := AlignClaimedFiles(ctx, j.desired, j.clock, workID); err != nil {
		j.logger.WarnContext(ctx, "align refined work failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
}

// chooseEdition picks the edition a release-group-level work's files fit best, refines it (building the tracklist), and reports whether files can now be matched.
func (j *EnrichJob) chooseEdition(
	ctx context.Context, work library.EditionlessWork, group musicbrainz.ReleaseGroup, artists artistMemo,
) bool {
	if j.tags == nil || j.roots == nil {
		return false
	}
	locations, err := j.identity.WorkFileLocations(ctx, []int64{work.WorkID})
	if err != nil || len(locations) == 0 {
		j.logger.InfoContext(ctx, "edition choice: no files to choose by", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
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
	choice, ok := chooseRelease(group, releases, locals)
	if !ok {
		// Without this the album waits for metadata with nothing saying why (found 2026-09-28).
		j.logger.InfoContext(ctx, "edition choice: no release has a tracklist", slog.Int64("work_id", work.WorkID),
			slog.Int("files", len(locals)), slog.Int("tags_read", len(probes)), slog.Int("releases", len(releases)))
		return false
	}
	if !choice.Fits {
		j.logger.InfoContext(ctx, "edition choice: no release fits the files, best ranked taken", slog.Int64("work_id", work.WorkID),
			slog.Int("files", len(locals)), slog.String("release", choice.Release.MBID))
	}
	release := choice.Release

	resolutions, err := j.identity.ResolveWorksDetailed(ctx, j.clock.Now(), []library.WorkLink{{
		Provider: ProviderReleaseGroup, ExternalID: work.ReleaseGroupMBID, Confidence: siblingConfidence,
		Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: release.Title, SortTitle: library.SortTitle(release.Title)},
		Edition: &library.EditionLink{
			Provider: ProviderRelease, ExternalID: release.MBID, Confidence: siblingConfidence,
			Edition: library.EditionIdentity{Kind: "unknown", Label: release.MBID},
		},
	}})
	if err != nil || len(resolutions) != 1 || resolutions[0].EditionID == nil {
		j.logger.WarnContext(ctx, "edition choice resolve failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	if owner, err := j.identity.EditionWork(ctx, *resolutions[0].EditionID); err != nil || owner != work.WorkID {
		// Refining it would enrich the other album and leave this one without an edition, every run.
		j.logger.InfoContext(ctx, "edition choice: the release fitting the files is another album's; the two are one album held twice",
			slog.Int64("work_id", work.WorkID), slog.Int64("other_work_id", owner), slog.Any("error", err))
		if err == nil {
			if err := j.identity.SuggestWorkMerge(ctx, work.WorkID, owner); err != nil {
				j.logger.WarnContext(ctx, "merge suggestion failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
			}
		}
		return false
	}
	chosen := albumKey{releaseGroup: work.ReleaseGroupMBID, release: release.MBID}
	if err := j.enrichOne(ctx, library.PlaceholderEdition{
		EditionID: *resolutions[0].EditionID, WorkID: work.WorkID, ExternalID: release.MBID,
	}, j.lookupAlbums(ctx, []albumKey{chosen})[0], artists); err != nil {
		j.logger.WarnContext(ctx, "chosen edition refine failed", slog.Int64("work_id", work.WorkID), slog.Any("error", err))
		return false
	}
	return true
}

// backfillProgress is one run's artist backfill: what it may still spend, and where it stopped,
// so a later slice moves on past artists that failed rather than retrying them.
type backfillProgress struct {
	budget                 int
	afterPersonID          int64
	filled, absent, failed int
}

// backfillPeople gives artists resolved before their image and biography had a source both
// (found 2026-09-13), at most slice of them now and never past the run's budget.
func (j *EnrichJob) backfillPeople(ctx context.Context, progress *backfillProgress, slice int) {
	// Without a Wikidata source every artist would look imageless and be
	// recorded so, permanently.
	if j.artworks == (library.Artworks{}) || !j.wikidataOn() {
		return
	}

	spent := 0
	for spent < slice && progress.budget > 0 {
		if ctx.Err() != nil {
			return
		}

		batch, err := j.artworks.PeopleNeedingArtwork(ctx, progress.afterPersonID, personBackfillBatchSize,
			j.clock.Now().Add(-personArtworkAbsentTTL).Unix())
		if err != nil {
			j.logger.WarnContext(ctx, "people needing artwork failed", slog.Any("error", err))
			return
		}
		if len(batch) == 0 {
			break
		}

		for _, candidate := range batch {
			if spent >= slice || progress.budget <= 0 {
				break
			}
			progress.afterPersonID = candidate.PersonID
			spent++
			progress.budget--
			switch outcome := j.backfillOne(ctx, candidate); outcome {
			case backfillFilled:
				progress.filled++
			case backfillAbsent:
				progress.absent++
			default:
				progress.failed++
			}
		}
		if len(batch) < personBackfillBatchSize {
			break
		}
	}

	if spent > 0 {
		j.logger.Info("artist backfill finished",
			slog.Int("filled", progress.filled), slog.Int("no_image", progress.absent), slog.Int("failed", progress.failed))
	}
}

type backfillOutcome int

const (
	backfillFailed backfillOutcome = iota
	backfillFilled
	backfillAbsent
)

// backfillOne resolves one person's provider detail; a definitive no-image answer is recorded so the sweep reaches the rest, a transient failure is retried.
func (j *EnrichJob) backfillOne(ctx context.Context, candidate library.PersonArtworkCandidate) backfillOutcome {
	ctx = j.forPerson(ctx, candidate.PersonID)
	artist, err := j.provider.Artist(ctx, musicbrainz.PriorityEnrich, candidate.ArtistMBID)
	if err != nil {
		j.logger.WarnContext(ctx, "artist backfill lookup failed",
			slog.Int64("person_id", candidate.PersonID), slog.String("mbid", candidate.ArtistMBID),
			slog.Any("error", err))
		return backfillFailed
	}

	j.linkWikidata(ctx, candidate.PersonID, artist.WikidataQID)
	bio, err := j.wikidataLookup(ctx, artist)
	var artErr error
	if err == nil {
		bio, artErr = j.withArtistArt(ctx, artist, bio)
	}
	if err != nil {
		// A failed lookup says nothing about whether the artist has a photo.
		j.logger.WarnContext(ctx, "artist backfill wikidata lookup failed",
			slog.Int64("person_id", candidate.PersonID), slog.String("qid", artist.WikidataQID),
			slog.Any("error", err))
		return backfillFailed
	}
	if err := j.people.BackfillDetails(ctx, candidate.PersonID, library.PersonIdentity{
		Name: artist.Name, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
		Born: artist.Born, Died: artist.Died, Biography: biographyText(bio), Aliases: artist.Aliases,
	}); err != nil {
		j.logger.WarnContext(ctx, "artist backfill details failed",
			slog.Int64("person_id", candidate.PersonID), slog.Any("error", err))
	}

	if artErr != nil && bio.ImageURL == "" {
		// The second source failing says nothing about whether the artist has a photo.
		j.logger.WarnContext(ctx, "artist backfill second source failed",
			slog.Int64("person_id", candidate.PersonID), slog.Any("error", artErr))
		return backfillFailed
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

// linkWikidata keeps the artist's QID, which also folds in the same human known from another library.
func (j *EnrichJob) linkWikidata(ctx context.Context, personID int64, qid string) {
	if qid == "" {
		return
	}
	if _, err := j.people.LinkWikidata(ctx, personID, qid); err != nil {
		j.logger.WarnContext(ctx, "artist wikidata link failed", slog.Int64("person_id", personID), slog.Any("error", err))
	}
}

// replaceRetiredRelease handles an edition whose release no source knows any more: while its
// release group lives, an edition is chosen by the files, as for a text-search match, and the
// dead one withdrawn. A retired release group sends the album's files back to Review.
func (j *EnrichJob) replaceRetiredRelease(
	ctx context.Context, edition library.PlaceholderEdition, album albumKey, artists artistMemo,
) bool {
	if album.releaseGroup == "" {
		return false
	}
	group, _, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, album.releaseGroup, "")
	if errors.Is(err, musicbrainz.ErrNotFound) {
		j.returnRetiredToReview(ctx, edition.WorkID, album.releaseGroup)
		return false
	}
	if err != nil || !j.chooseEdition(ctx, library.EditionlessWork{WorkID: edition.WorkID, ReleaseGroupMBID: album.releaseGroup}, group, artists) {
		return false
	}
	if err := j.identity.WithdrawEdition(ctx, edition.EditionID); err != nil {
		j.logger.WarnContext(ctx, "withdraw retired edition failed", slog.Int64("edition_id", edition.EditionID), slog.Any("error", err))
	}
	j.logger.InfoContext(ctx, "retired release replaced by one chosen from the files",
		slog.Int64("work_id", edition.WorkID), slog.String("retired_release", edition.ExternalID))
	return true
}

// retiredCandidateConfidence keeps a retired release group a suggestion in Review, never a match.
const retiredCandidateConfidence = 0.2

// returnRetiredToReview hands an album whose release group exists nowhere any more back to Review:
// its files need identifying again, with the retired id as a weak hint (M1f).
func (j *EnrichJob) returnRetiredToReview(ctx context.Context, workID int64, releaseGroupMBID string) {
	returned, err := j.identity.ReturnToReview(ctx, workID, model.Identification{
		Strategy: "retired release group", Confidence: retiredCandidateConfidence,
		ExternalID: &model.ExternalID{
			Provider: ProviderReleaseGroup, ExternalID: releaseGroupMBID, Confidence: retiredCandidateConfidence,
		},
	}, model.ExternalID{Provider: ProviderLocalAlbum, ExternalID: j.unverifiedKey(ctx, workID, releaseGroupMBID), Confidence: unverifiedConfidence},
		j.clock.Now().Unix())
	if err != nil {
		j.logger.WarnContext(ctx, "retired release group: files not returned to review",
			slog.Int64("work_id", workID), slog.Any("error", err))
		return
	}
	j.logger.InfoContext(ctx, "release group retired: the album's files went back to Review",
		slog.Int64("work_id", workID), slog.String("title", returned.Title), slog.String("release_group", releaseGroupMBID),
		slog.Int("files", returned.Files), slog.Bool("kept_unverified", returned.Unverified))
}

// unverifiedKey is the local name a scan of the album's folder would give it, so a rescan finds the same album.
func (j *EnrichJob) unverifiedKey(ctx context.Context, workID int64, releaseGroupMBID string) string {
	work, err := j.identity.WorkIdentityOf(ctx, workID)
	locations, locationsErr := j.identity.WorkFileLocations(ctx, []int64{workID})
	if err != nil || locationsErr != nil || len(locations) == 0 || j.roots == nil {
		return "retired:" + releaseGroupMBID
	}
	root, found := j.roots.RootFolder(locations[0].RootFolderID)
	if !found {
		return "retired:" + releaseGroupMBID
	}
	return localAlbumKey(root.Path, path.Dir(locations[0].RelPath), work.CanonicalTitle)
}

// albumKey is one album lookup, in its library's provider order (0: the caller's); err set means there is nothing to look up.
type albumKey struct {
	releaseGroup, release string
	library               int64
	err                   error
}

// albumAnswer is what Provider.Album answered for one albumKey, with the album's description.
type albumAnswer struct {
	rg          musicbrainz.ReleaseGroup
	release     musicbrainz.Release
	err         error
	description wikidata.Biography
	// genres are Wikidata's, which lead MusicBrainz's where there are any.
	genres []string
}

// placeholderAlbum names the lookup a placeholder edition needs.
func (j *EnrichJob) placeholderAlbum(ctx context.Context, edition library.PlaceholderEdition) albumKey {
	releaseGroupMBID, ok, err := j.identity.WorkExternalID(ctx, edition.WorkID, ProviderReleaseGroup)
	if err != nil {
		return albumKey{err: fmt.Errorf("release group id: %w", err)}
	}
	if !ok {
		// newEditionLink links the release group in the same transaction; a missing one is not ours to guess.
		return albumKey{err: fmt.Errorf("work %d has no %s external id", edition.WorkID, ProviderReleaseGroup)}
	}
	return albumKey{releaseGroup: releaseGroupMBID, release: edition.ExternalID, library: j.workLibrary(ctx, edition.WorkID)}
}

// lookupAlbums asks the provider for every album at once, enrichLookupWorkers at a time; the
// rate-limit bucket still paces them. Answers keep the order of albums.
func (j *EnrichJob) lookupAlbums(ctx context.Context, albums []albumKey) []albumAnswer {
	answers := make([]albumAnswer, len(albums))
	looked := parallel.Do(ctx, len(albums), enrichLookupWorkers, func(ctx context.Context, index int) error {
		album := albums[index]
		if album.err != nil {
			return album.err
		}
		if album.library != 0 {
			ctx = musicbrainz.ForLibrary(ctx, album.library)
		}
		rg, release, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, album.releaseGroup, album.release)
		if err != nil {
			err = fmt.Errorf("musicbrainz: %w", err)
		}
		answers[index] = albumAnswer{rg: rg, release: release, err: err}
		return nil
	})
	for index, err := range looked {
		if err != nil {
			answers[index].err = err
		}
	}
	j.describeAlbums(ctx, answers)
	return answers
}

// followMergedIDs repoints a work or edition whose MusicBrainz id was merged into another: the old
// id still answers (a redirect), but the new one is what MusicBrainz and everyone else now use.
func (j *EnrichJob) followMergedIDs(ctx context.Context, edition library.PlaceholderEdition, asked albumKey, answered albumAnswer) {
	workMoved, editionMoved, err := j.identity.FollowMergedIDs(ctx, library.MergedIDs{
		WorkID: edition.WorkID, WorkProvider: ProviderReleaseGroup, OldWorkID: asked.releaseGroup, NewWorkID: answered.rg.MBID,
		EditionID: edition.EditionID, EditionProvider: ProviderRelease, OldEditionID: asked.release,
		NewEditionID: answered.release.MBID,
	})
	switch {
	case err != nil:
		j.logger.WarnContext(ctx, "merged id not followed", slog.Int64("work_id", edition.WorkID), slog.Any("error", err))
	case workMoved || editionMoved:
		j.logger.InfoContext(ctx, "merged MusicBrainz id followed", slog.Int64("work_id", edition.WorkID),
			slog.String("release_group", asked.releaseGroup+" -> "+answered.rg.MBID),
			slog.String("release", asked.release+" -> "+answered.release.MBID))
	}
}

// describeAlbums fetches every answered album's Wikidata description in one batch: one album at a
// time, Wikidata's 1 req/s capped enrichment at ~1,800 albums an hour (measured 2026-09-28).
func (j *EnrichJob) describeAlbums(ctx context.Context, answers []albumAnswer) {
	if j.biography == nil {
		return
	}
	var qids []string
	for _, answer := range answers {
		if answer.err == nil && answer.rg.WikidataQID != "" {
			qids = append(qids, answer.rg.WikidataQID)
		}
	}
	if len(qids) == 0 {
		return
	}
	descriptions, err := j.biography.Biographies(ctx, qids)
	if err != nil {
		j.logger.WarnContext(ctx, "work descriptions lookup failed", slog.Int("albums", len(qids)), slog.Any("error", err))
		return
	}
	genres, err := j.biography.GenresOf(ctx, qids)
	if err != nil {
		j.logger.WarnContext(ctx, "album genres lookup failed", slog.Int("albums", len(qids)), slog.Any("error", err))
	}
	for index := range answers {
		if answers[index].err == nil {
			answers[index].description = descriptions[answers[index].rg.WikidataQID]
			answers[index].genres = genres[answers[index].rg.WikidataQID]
		}
	}
}

func (j *EnrichJob) enrichOne(
	ctx context.Context, edition library.PlaceholderEdition, album albumAnswer, artists artistMemo,
) error {
	rg, release, err := album.rg, album.release, album.err
	if err != nil {
		return err
	}

	workYear, workReleaseDate, workPrimaryType, refinement, parts := refinementFor(rg, release)
	if err := j.identity.RefineEdition(ctx, j.clock.Now().Unix(),
		edition.EditionID, edition.WorkID, workYear, workReleaseDate, workPrimaryType, refinement, parts); err != nil {
		return fmt.Errorf("refine: %w", err)
	}

	// Best-effort: a credit, genre or description that fails to resolve is not
	// a reason to fail an edition that otherwise refined correctly. Logged,
	// not returned.
	j.retitle(ctx, edition.WorkID, rg)
	j.enrichCreditsAndGenres(ctx, edition.WorkID, rg, artists)
	j.enrichDescription(ctx, edition.WorkID, album)
	return nil
}

// enrichCreditsAndGenres writes ADR-32 credits and ADR-33 genres from the release group already fetched; Provider.Artist caches permanently, so one lookup per artist.
func (j *EnrichJob) enrichCreditsAndGenres(
	ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup, artists artistMemo,
) {
	for index, credit := range rg.ArtistCredit {
		personID, err := j.resolveArtist(ctx, credit, artists)
		if err != nil {
			j.logger.WarnContext(ctx, "artist credit resolve failed",
				slog.String("mbid", credit.MBID), slog.Any("error", err))
			continue
		}
		if index == 0 {
			err = j.people.AddLeadCredit(ctx, workID, personID, CreditRoleArtist)
		} else {
			err = j.people.AddCredit(ctx, workID, personID, CreditRoleArtist, index)
		}
		if err != nil {
			j.logger.WarnContext(ctx, "add credit failed",
				slog.Int64("work_id", workID), slog.Int64("person_id", personID), slog.Any("error", err))
		}
	}

	if rg.Rating.Votes > 0 {
		// MusicBrainz rates 0 to 5; the library keeps every provider's on 0 to 10.
		if err := j.people.SetRating(ctx, workID, "musicbrainz", rg.Rating.Value*2, int64(rg.Rating.Votes), j.clock.Now().Unix()); err != nil {
			j.logger.WarnContext(ctx, "set rating failed", slog.Int64("work_id", workID), slog.Any("error", err))
		}
	}

	j.rateFromOthers(ctx, workID, rg)

	if len(rg.Aliases) > 0 {
		if err := j.people.SetAliases(ctx, workID, "musicbrainz", rg.Aliases, j.clock.Now().Unix()); err != nil {
			j.logger.WarnContext(ctx, "set aliases failed", slog.Int64("work_id", workID), slog.Any("error", err))
		}
	}

	names := weightedGenres(rg.Genres)
	if len(names) == 0 {
		// Folksonomy tags stand in where nobody curated a genre.
		names = weightedGenres(rg.Tags)
	}
	source := library.GenreSourceMusicBrainz
	if len(names) == 0 {
		// An album nobody voted genres onto takes its lead artist's, under a source of their own that a later real genre replaces.
		names, source = leadArtistGenres(rg, artists), library.GenreSourceArtist
		if len(names) == 0 {
			return
		}
	} else if err := j.people.ClearGenresFrom(ctx, workID, library.GenreSourceArtist); err != nil {
		j.logger.WarnContext(ctx, "clear artist genres failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
	genres := make([]library.GenreLink, len(names))
	for index, name := range names {
		genres[index] = library.GenreLink{Name: name}
	}
	if err := j.people.AssignGenresFrom(ctx, workID, GenreTree.Key, source, genres); err != nil {
		j.logger.WarnContext(ctx, "assign genres failed", slog.Int64("work_id", workID), slog.Any("error", err))
	}
}

// leadArtistGenres are the weighted genres of the release group's first credited artist, resolved a moment ago.
func leadArtistGenres(rg musicbrainz.ReleaseGroup, artists artistMemo) []string {
	if len(rg.ArtistCredit) == 0 {
		return nil
	}
	return artists.genres[rg.ArtistCredit[0].MBID]
}

// maxAlbumGenres is the most genres one album takes from the votes.
const maxAlbumGenres = 6

// weightedGenres are the genres the votes agree on: the most voted first, those with a quarter of the top count or
// more, at most maxAlbumGenres. Counts of one or none are all single votes and all stay.
func weightedGenres(genres []musicbrainz.Genre) []string {
	ranked := slices.Clone(genres)
	slices.SortStableFunc(ranked, func(a, b musicbrainz.Genre) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Name, b.Name)
	})
	var names []string
	for _, genre := range ranked {
		if len(names) == maxAlbumGenres || (len(names) > 0 && ranked[0].Count > 1 && genre.Count < ranked[0].Count/4) {
			break
		}
		names = append(names, genre.Name)
	}
	return names
}

// enrichDescription uses a Wikipedia extract when the QID has an English article, else Wikidata's short description, else nothing; MusicBrainz annotations proved empty and are not used.
func (j *EnrichJob) enrichDescription(ctx context.Context, workID int64, album albumAnswer) {
	// Wikidata's genres are curated where MusicBrainz's are voted tags; written after MusicBrainz's, they replace them.
	if len(album.genres) > 0 {
		genres := make([]library.GenreLink, len(album.genres))
		for index, name := range album.genres {
			genres[index] = library.GenreLink{Name: name}
		}
		if err := j.people.AssignGenresFrom(ctx, workID, GenreTree.Key, library.GenreSourceWikidata, genres); err != nil {
			j.logger.WarnContext(ctx, "assign wikidata genres failed", slog.Int64("work_id", workID), slog.Any("error", err))
		}
	}
	bio := album.description
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

// resolveArtist finds or creates the person behind a credit, from the credit's own name and id if the Artist lookup fails. artistMemo caches artists per run, as each costs a Wikidata request (~1.2 s an album).
type artistMemo struct {
	ids map[string]int64
	// genres are each looked-up artist's weighted genres, for an album nobody voted genres onto.
	genres map[string][]string
}

func newArtistMemo() artistMemo {
	return artistMemo{ids: map[string]int64{}, genres: map[string][]string{}}
}

func (j *EnrichJob) resolveArtist(ctx context.Context, credit musicbrainz.ArtistCredit, artists artistMemo) (int64, error) {
	if credit.MBID == "" {
		return 0, fmt.Errorf("artist credit %q has no id", credit.Name)
	}
	if personID, done := artists.ids[credit.MBID]; done {
		return personID, nil
	}

	artist, err := j.provider.Artist(ctx, musicbrainz.PriorityEnrich, credit.MBID)
	lookedUp := err == nil
	if err != nil {
		j.logger.WarnContext(ctx, "artist detail lookup failed",
			slog.String("mbid", credit.MBID), slog.Any("error", err))
		artist = musicbrainz.Artist{MBID: credit.MBID, Name: credit.Name, SortName: credit.Name}
	}

	bio := j.wikidataBiographyFor(ctx, artist)
	identity := library.PersonIdentity{
		Name: artist.Name, Disambiguation: artist.Disambiguation,
		Kind: artist.Type, Gender: artist.Gender, Country: artist.Country,
		Born: artist.Born, Died: artist.Died, Biography: biographyText(bio), Aliases: artist.Aliases,
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
	// Best-effort like the biography: a person without an image is still worth having; fetching it here lets album covers fall back to it.
	_ = j.fetchPersonImage(ctx, personID, bio.ImageURL)
	artists.ids[credit.MBID] = personID
	artists.genres[credit.MBID] = weightedGenres(artist.Genres)
	return personID, nil
}

// wikidataBiographyFor is one lookup feeding both the biography text and the image; it returns the zero Biography when nothing is available and never fails resolveArtist.
func (j *EnrichJob) wikidataBiographyFor(ctx context.Context, artist musicbrainz.Artist) wikidata.Biography {
	bio, err := j.wikidataLookup(ctx, artist)
	if err != nil {
		j.logger.WarnContext(ctx, "biography lookup failed",
			slog.String("qid", artist.WikidataQID), slog.Any("error", err))
		bio = wikidata.Biography{}
	}
	bio, err = j.withArtistArt(ctx, artist, bio)
	if err != nil {
		j.logger.DebugContext(ctx, "second artist source failed", slog.String("mbid", artist.MBID), slog.Any("error", err))
	}
	return bio
}

// withArtistArt fills the photo and biography Wikidata left empty from the second source; the error is the
// source failing, not knowing nothing (that answers empty).
func (j *EnrichJob) withArtistArt(ctx context.Context, artist musicbrainz.Artist, bio wikidata.Biography) (wikidata.Biography, error) {
	if j.artistArt == nil || artist.MBID == "" || (bio.ImageURL != "" && biographyText(bio) != "") {
		return bio, nil
	}
	photo, text, err := j.artistArt(ctx, artist.MBID)
	if err != nil {
		return bio, err
	}
	if bio.ImageURL == "" {
		bio.ImageURL = photo
	}
	if biographyText(bio) == "" {
		bio.Extract = text
	}
	return bio, nil
}

// wikidataLookup is wikidataBiographyFor without swallowing the error, for a
// caller that must not mistake a failed lookup for an artist with no image.
func (j *EnrichJob) wikidataLookup(ctx context.Context, artist musicbrainz.Artist) (wikidata.Biography, error) {
	if !j.wikidataOn() || artist.WikidataQID == "" {
		return wikidata.Biography{}, nil
	}
	return j.biography.Biography(ctx, artist.WikidataQID)
}

// wikidataOn is whether there is a Wikidata source and the user has not switched it off.
func (j *EnrichJob) wikidataOn() bool {
	return j.biography != nil && (j.wikidataEnabled == nil || j.wikidataEnabled())
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

// fetchPersonImage downloads and records a person's Wikidata photo, duplicating ArtworkJob.processOne (two callers, below the extract threshold); never fails the credit.
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
	source, err := httpx.ReadLimited(response.Body, maxPersonImageBytes)
	if err != nil {
		j.logger.WarnContext(ctx, "person image read failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageFailed
	}

	// The same bytes will fail to decode next run too, so this is final.
	sizes, err := artwork.Process(ctx, source)
	if err != nil {
		j.logger.WarnContext(ctx, "person image process failed",
			slog.Int64("person_id", personID), slog.Any("error", err))
		return imageUnusable
	}

	cacheKey := fmt.Sprintf("person-%d-cover", personID)
	for size, image := range sizes {
		destination, err := artwork.ShardedFor(j.fs, j.imagesDir, fmt.Sprintf("%s-%s.webp", cacheKey, artworkSizeName(size)))
		if err != nil {
			j.logger.WarnContext(ctx, "person image folder not made", slog.Int64("person_id", personID), slog.Any("error", err))
			return imageFailed
		}
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
func EnqueueEnrichment(ctx context.Context, queue *jobs.Queue, manual bool) (jobs.EnqueueResult, error) {
	payload, err := json.Marshal(EnrichPayload{Manual: manual})
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	return queue.Enqueue(ctx, jobs.Request{
		Type: EnrichJobType, ActionKey: EnrichActionKey, Priority: jobs.PriorityLow, Payload: payload,
	})
}

// EnrichPayload says whether a person asked for this run ("Run now"), which a library's
// "Rescan Artist Folder after Refresh: Only After Manual Refresh" depends on.
type EnrichPayload struct {
	Manual bool `json:"manual,omitempty"`
}
