package music

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"path/filepath"
	"sync"

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
	// Biographies answers many QIDs at once; a QID it has nothing for is absent.
	Biographies(ctx context.Context, qids []string) (map[string]wikidata.Biography, error)
}

// ArtistArt is a second source of an artist's photo and biography, by MusicBrainz artist id
// (TheAudioDB, 2026-09-25): asked only for what Wikidata left empty.
type ArtistArt func(ctx context.Context, artistMBID string) (photoURL, biography string, err error)

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

// enrichLookupWorkers overlaps a batch's album lookups: BrainzMash answers in ~1 s but allows
// 10 req/s, so one at a time left enrichment at a tenth of its budget (measured 2026-09-28).
const enrichLookupWorkers = 8

// personBackfillBatchSize is one page of the artist image and biography sweep; the sweep's
// per-run budget is a setting (EnrichJobConfig.PeopleBackfillPerRun).
const personBackfillBatchSize = 20

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
	provider        musicbrainz.Provider
	biography       Biographer
	artworks        library.Artworks
	realigner       Realigner
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
	backfillPerRun  func() int
	checkpointEvery int
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
	RealignWorks(ctx context.Context, workIDs []int64, manual bool) error
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
	// WikidataEnabled is the configured switch, read per artist; nil is on.
	WikidataEnabled func() bool
	// PeopleBackfillPerRun is the configured budget, read per run; nil is 200.
	PeopleBackfillPerRun func() int
	// ArtistArt is optional: without it an artist Wikidata has no photo for keeps none.
	ArtistArt ArtistArt
	Artworks  library.Artworks
	// Realigner is optional: without one, refined albums wait for the next
	// scan that happens to revisit their directory.
	Realigner Realigner
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
		identity: config.Identity, people: config.People, provider: config.Provider,
		biography: config.Biography, wikidataEnabled: config.WikidataEnabled,
		backfillPerRun: config.PeopleBackfillPerRun, checkpointEvery: config.CheckpointBatches,
		artworks: config.Artworks, realigner: config.Realigner, desired: config.Desired, onRefined: config.OnRefined,
		artistArt: config.ArtistArt,
		tags:      config.Tags, roots: config.Roots, http: config.HTTP,
		fs: config.FS, imagesDir: config.ImagesDir, clock: config.Clock, logger: logger,
	}
}

// Run processes every placeholder edition it can find, in bounded batches, until
// none remain or the context is cancelled. There is no persisted cursor: a
// refined edition's kind moves off 'unknown', so PlaceholderEditions' own result
// set shrinks as work succeeds -- a restart simply asks the same question again
// and gets whatever is left (library.PlaceholderEditions's doc comment).
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
	artists := artistMemo{}
	total, failed, batches := 0, 0, 0
	var refinedWorkIDs []int64
	var afterEditionID int64
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
			if err := j.enrichOne(ctx, edition, answers[index], artists); err == nil {
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
		if len(batch) < enrichBatchSize {
			break
		}
		if batches++; batches%checkpointEvery == 0 {
			j.handOn(ctx, refinedWorkIDs, payload.Manual)
			refinedWorkIDs = nil
			j.backfillPeople(ctx, &backfill, personBackfillBatchSize)
		}
	}
	j.logger.Info("music enrichment finished", slog.Int("processed", total), slog.Int("failed", failed))

	refinedWorkIDs = append(refinedWorkIDs, j.enrichEditionless(ctx, artists)...)
	j.handOn(ctx, refinedWorkIDs, payload.Manual)
	j.backfillPeople(ctx, &backfill, backfill.budget)
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

// enrichEditionless gives works identified at release-group level -- the text
// search's output, which has no edition -- their artist, genres, description,
// year and type. The placeholder-edition pass above never reached them, so
// they sat under "Unknown artist" (found 2026-09-17). No tracklist: that needs
// an edition, which choosing is not this pass's call. Walked by work id within
// a run and bounded by editionlessBudget.
func (j *EnrichJob) enrichEditionless(ctx context.Context, artists artistMemo) (withEdition []int64) {
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
		batch = batch[:min(len(batch), editionlessBudget-enriched-failed)]
		albums := make([]albumKey, len(batch))
		for index, work := range batch {
			albums[index] = albumKey{releaseGroup: work.ReleaseGroupMBID}
		}
		answers := j.lookupAlbums(ctx, albums)
		for index, work := range batch {
			afterWorkID = work.WorkID
			rg, err := answers[index].rg, answers[index].err
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
			j.retitle(ctx, work.WorkID, rg)
			j.enrichCreditsAndGenres(ctx, work.WorkID, rg, artists)
			j.enrichDescription(ctx, work.WorkID, answers[index].description)
			enriched++
			if j.chooseEdition(ctx, work, rg, artists) {
				withEdition = append(withEdition, work.WorkID)
				j.alignClaimed(ctx, work.WorkID)
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

// retitle names the work after its release group; best-effort, like the credits.
func (j *EnrichJob) retitle(ctx context.Context, workID int64, rg musicbrainz.ReleaseGroup) {
	if rg.Title == "" {
		return
	}
	if err := j.identity.RetitleWork(ctx, j.clock.Now().Unix(), workID, rg.Title, SortTitle(rg.Title)); err != nil {
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

// chooseEdition gives a work identified only at release-group level the
// edition its files fit best (chooseRelease, approved 2026-09-17), then refines
// it like any placeholder edition, which builds the tracklist. True when an
// edition was chosen and refined, so the work's files can be matched to it.
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
	release, ok := chooseRelease(group, releases, locals)
	if !ok {
		// Without this the album waits for metadata with nothing saying why (found 2026-09-28).
		j.logger.InfoContext(ctx, "edition choice: no release fits the files", slog.Int64("work_id", work.WorkID),
			slog.Int("files", len(locals)), slog.Int("tags_read", len(probes)), slog.Int("releases", len(releases)))
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

		batch, err := j.artworks.PeopleNeedingArtwork(ctx, progress.afterPersonID, personBackfillBatchSize)
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
	if err == nil {
		bio = j.withArtistArt(ctx, artist, bio)
	}
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

// albumKey is one album lookup; err set means there is nothing to look up.
type albumKey struct {
	releaseGroup, release string
	err                   error
}

// albumAnswer is what Provider.Album answered for one albumKey, with the album's description.
type albumAnswer struct {
	rg          musicbrainz.ReleaseGroup
	release     musicbrainz.Release
	err         error
	description wikidata.Biography
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
	return albumKey{releaseGroup: releaseGroupMBID, release: edition.ExternalID}
}

// lookupAlbums asks the provider for every album at once, enrichLookupWorkers at a time; the
// rate-limit bucket still paces them. Answers keep the order of albums.
func (j *EnrichJob) lookupAlbums(ctx context.Context, albums []albumKey) []albumAnswer {
	answers := make([]albumAnswer, len(albums))
	slots := make(chan struct{}, enrichLookupWorkers)
	var wait sync.WaitGroup
	for index, album := range albums {
		if album.err != nil {
			answers[index].err = album.err
			continue
		}
		slots <- struct{}{}
		wait.Add(1)
		go func() {
			defer wait.Done()
			defer func() { <-slots }()
			rg, release, err := j.provider.Album(ctx, musicbrainz.PriorityEnrich, album.releaseGroup, album.release)
			if err != nil {
				err = fmt.Errorf("musicbrainz: %w", err)
			}
			answers[index] = albumAnswer{rg: rg, release: release, err: err}
		}()
	}
	wait.Wait()
	j.describeAlbums(ctx, answers)
	return answers
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
	for index := range answers {
		if answers[index].err == nil {
			answers[index].description = descriptions[answers[index].rg.WikidataQID]
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
	j.enrichDescription(ctx, edition.WorkID, album.description)
	return nil
}

// enrichCreditsAndGenres populates ADR-32's role="artist" credits and ADR-33's
// genre tags from the same release group Album already fetched -- no extra
// request for the genres (inc=genres rides along with artist-credits), one
// more request per distinct artist for their own bio facts (Provider.Artist
// caches permanently, so a prolific artist only ever costs one real lookup
// across every edition of theirs this sweep touches).
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
func (j *EnrichJob) enrichDescription(ctx context.Context, workID int64, bio wikidata.Biography) {
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
// artistMemo is the artists one run has already resolved, by MusicBrainz id: their biography and
// photo cost a Wikidata request each, once per album before, ~1.2 s an album (measured 2026-09-28).
type artistMemo map[string]int64

func (j *EnrichJob) resolveArtist(ctx context.Context, credit musicbrainz.ArtistCredit, artists artistMemo) (int64, error) {
	if credit.MBID == "" {
		return 0, fmt.Errorf("artist credit %q has no id", credit.Name)
	}
	if personID, done := artists[credit.MBID]; done {
		return personID, nil
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
	artists[credit.MBID] = personID
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
		bio = wikidata.Biography{}
	}
	return j.withArtistArt(ctx, artist, bio)
}

// withArtistArt fills the photo and biography Wikidata left empty from the second source.
func (j *EnrichJob) withArtistArt(ctx context.Context, artist musicbrainz.Artist, bio wikidata.Biography) wikidata.Biography {
	if j.artistArt == nil || artist.MBID == "" || (bio.ImageURL != "" && biographyText(bio) != "") {
		return bio
	}
	photo, text, err := j.artistArt(ctx, artist.MBID)
	if err != nil {
		j.logger.DebugContext(ctx, "second artist source had nothing", slog.String("mbid", artist.MBID), slog.Any("error", err))
		return bio
	}
	if bio.ImageURL == "" {
		bio.ImageURL = photo
	}
	if biographyText(bio) == "" {
		bio.Extract = text
	}
	return bio
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
