package app

import (
	"context"
	"log/slog"
	"os/user"
	"path"
	"path/filepath"
	"strconv"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/importing/placer"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/adult"
	"github.com/makearr/Solarr/internal/media/book"
	"github.com/makearr/Solarr/internal/media/movie"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// registerImportJob wires ADR-48's import.inspect job: each media type imports a batch its own way, and a session
// with no library yet is identified as music, which is what an unsorted download folder mostly is.
func (p *process) registerImportJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(importjob.Definition); err != nil {
		return err
	}
	job := importjob.NewJob(importjob.Config{
		FS:         p.fsys,
		Sessions:   library.NewImportSessions(p.library),
		Policy:     adapters.ImportPolicy{Cache: p.cache},
		Packs:      library.NewPacks(p.library),
		Importers:  p.batchImporters(),
		Unassigned: string(music.MediaType),
		MediaTypeOf: func(categoryID int64) string {
			if category, found := p.cache.Snapshot().Category(categoryID); found {
				return category.MediaType
			}
			return ""
		},
		Libraries:       p.suggester().ImportLibraries,
		Tracked:         p.trackedDownloadAt,
		Acquisitions:    library.NewAcquisitions(p.library),
		MaxArchiveBytes: func() int64 { return p.cache.Snapshot().ImportMaxArchiveBytes() },
		Workspace:       filepath.Join(p.config.Paths.CacheDir, "import-workspace"),
		// The bin sits on the source's own disk, so an archive is renamed into it, never copied.
		RecycleArchive: p.recycleSourceArchive,
		Events:         p.bus,
		// A new edition's tracklist is what lets the placed files count towards the album.
		OnCommitted: func(ctx context.Context) {
			_, _ = p.enrichmentPass(ctx, false)
			_, _ = p.enqueueCategoryCounts(ctx)
			p.enqueueAnalyze(ctx)
		},
		Clock:  clock.NewSystem(),
		Logger: p.logger,
	})
	executor.Handle(importjob.JobType, job)
	if err := registry.Register(importjob.RetryDefinition); err != nil {
		return err
	}
	executor.Handle(importjob.RetryJobType, importjob.NewRetryJob(library.NewImportSessions(p.library), p.manualCommitter(), p.logger))
	if err := registry.Register(importjob.CommitDefinition); err != nil {
		return err
	}
	executor.Handle(importjob.CommitJobType, importjob.NewCommitJob(library.NewImportSessions(p.library), p.manualCommitter(), p.bus, p.logger))
	return nil
}

// importRunningFor says a download's import job is waiting or running.
func (p *process) importRunningFor(ctx context.Context, acquisitionID int64) bool {
	active, err := p.queue.HasActive(ctx, importjob.ActionKeyOf(importjob.Payload{AcquisitionID: &acquisitionID}))
	return err == nil && active
}

// trackedDownloadAt is the download still being followed whose folder is, or holds, sourcePath.
func (p *process) trackedDownloadAt(ctx context.Context, sourcePath string) (int64, bool) {
	active, err := library.NewAcquisitions(p.library).ListActive(ctx)
	if err != nil {
		return 0, false
	}
	for _, acquisition := range active {
		if acquisition.DownloadDir != "" && fsx.Within(acquisition.DownloadDir, sourcePath) {
			return acquisition.ID, true
		}
	}
	return 0, false
}

// closeOrphanedImportSessions fails the import sessions no job carries any more, so their downloads are not held for ever.
func (p *process) closeOrphanedImportSessions(ctx context.Context) {
	closed, err := importjob.CloseOrphanedSessions(ctx, library.NewImportSessions(p.library), p.queue.HasActive, clock.NewSystem().Now())
	if err != nil {
		p.logger.Warn("orphaned import sessions not closed", slog.Any("error", err))
		return
	}
	if closed > 0 {
		p.logger.Info("closed import sessions no job was carrying", slog.Int("count", closed))
	}
}

// importRetryTask is the registered sweep that places what a library could not take when it was imported.
func (p *process) importRetryTask() jobs.Task {
	return jobs.Task{
		Key: importjob.RetryScheduleKey, Name: "Retry imports a library could not take", DefaultCadence: importRetryScheduleCron,
		Tier: string(settings.TierNormal), Category: jobs.CategoryDownloads, Startup: jobs.StartupOn,
		Enqueue: func(ctx context.Context) (jobs.EnqueueResult, error) { return importjob.EnqueueRetry(ctx, p.queue) },
	}
}

// acquisitionReleaseGroups are the release groups of the album a download was grabbed for.
func (p *process) acquisitionReleaseGroups(ctx context.Context, acquisitionID int64) ([]string, error) {
	acquisition, err := library.NewAcquisitions(p.library).Get(ctx, acquisitionID)
	if err != nil || acquisition.DesiredItemID == nil {
		return nil, err
	}
	item, err := library.NewDesired(p.library).Get(ctx, *acquisition.DesiredItemID)
	if err != nil {
		return nil, err
	}
	detail, err := library.NewWorks(p.library).Detail(ctx, item.WorkID)
	if err != nil {
		return nil, err
	}
	var groups []string
	for _, external := range detail.ExternalIDs {
		if external.Provider == music.ProviderReleaseGroup {
			groups = append(groups, external.ExternalID)
		}
	}
	return groups, nil
}

// importCommitter builds ADR-48's COMMIT step fresh per call (ask, don't cache), shared by import.inspect's inline placement and manualCommitter so they never drift.
func (p *process) importCommitter() music.Committer {
	desired := library.NewDesired(p.library)
	people := library.NewPeople(p.library)
	return music.NewCommitter(music.CommitterConfig{
		FS:               p.fsys,
		Roots:            adapters.DrainRoots{Cache: p.cache},
		Affinity:         p.cache,
		Physical:         library.NewPhysical(p.library),
		Identity:         library.NewIdentity(p.library),
		Desired:          &desired,
		Categories:       adapters.Categories{Cache: p.cache},
		BlockedContent:   library.NewBlockedContentStore(p.library),
		Clock:            clock.NewSystem(),
		DefaultProfileID: p.defaultQualityProfileID,
		TransferLadder:   p.transferLadder,
		RenameOnImport:   p.renameOnImport,
		ExtraFiles:       p.extraFileExtensions,
		GroupID:          lookupGroupID,
		People:           &people,
		Logger:           p.logger,
	})
}

// transferLadder reads the configured order per commit, so changing it in the
// interface takes effect on the next import rather than the next restart.
func (p *process) transferLadder() []mover.PlaceMethod {
	configured := p.cache.Snapshot().ImportTransferLadder()
	ladder := make([]mover.PlaceMethod, 0, len(configured))
	for _, method := range configured {
		ladder = append(ladder, mover.PlaceMethod(method))
	}
	return ladder
}

// renameOnImport reads the switch per commit, the same live read the ladder
// above is.
func (p *process) renameOnImport() bool {
	return p.cache.Snapshot().ImportRenameOnImport()
}

// extraFileExtensions answers the extensions to bring along into a library, or none while its switch is off.
func (p *process) extraFileExtensions(categoryID int64) []string {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found {
		return nil
	}
	return adapters.MediaTypes{Registry: p.media}.ExtraFileExtensions(category)
}

// lookupGroupID resolves a library's "chown group" name in this container's own group database.
func lookupGroupID(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(group.Gid)
}

// mediaTool builds the ffprobe reader every import path needs, so manualCommitter builds the identical instance.
func (p *process) mediaTool() mediatool.Tool {
	return mediatool.New(nil, 0)
}

// manualCommitter is the Import screen's commit-what-was-held and place-once-assigned, through each type's importer.
func (p *process) manualCommitter() importjob.ManualCommitter {
	importers := p.batchImporters()
	return importjob.NewManualCommitter(p.fsys, library.NewImportSessions(p.library), library.NewPacks(p.library),
		func(categoryID int64) batch.Importer {
			category, found := p.cache.Snapshot().Category(categoryID)
			if !found {
				return nil
			}
			return importers[category.MediaType]
		}).WithReleases(library.NewAcquisitions(p.library), clock.NewSystem())
}

// batchImporters are each media type's own identify-and-place of an import, by type.
func (p *process) batchImporters() map[string]batch.Importer {
	importers := map[string]batch.Importer{
		string(music.MediaType): p.musicImporter(), tv.MediaType: p.tvImporter(), movie.MediaType: p.movieImporter(), adult.MediaType: p.adultImporter(),
	}
	for _, format := range book.Formats {
		importers[format.MediaType] = p.bookImporter(format)
	}
	return importers
}

// bookImporter is a book type's batch importer, placing through the same steps as a movie's.
func (p *process) bookImporter(format book.Format) book.Importer {
	categories := adapters.Categories{Cache: p.cache}
	identity := library.NewIdentity(p.library)
	return book.NewImporter(book.ImporterConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Series: library.NewSeries(p.library),
		Reader: library.NewSeriesReader(p.library), Identity: identity, Desired: library.NewDesired(p.library),
		Acquisitions: library.NewAcquisitions(p.library),
		Placer: placer.New(placer.Config{
			FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Identity: identity,
			Blocked: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			TransferLadder: p.transferLadder, GroupID: lookupGroupID,
		}),
		Resolver: p.bookResolver(format), Tags: p.mediaTool(),
		Categories: categories, Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		RenameOnImport: p.renameOnImport, ExtraFiles: p.extraFileExtensions, AfterPlacing: p.afterPlacing,
	})
}

// musicImporter is Lidarr's import: tags, MusicBrainz search, album matching and fingerprints, placed by importCommitter.
func (p *process) musicImporter() music.Importer {
	return music.NewImporter(music.ImporterConfig{
		Tool: p.mediaTool(), Search: p.musicBrainzProvider(), Albums: p.musicBrainzProvider(), AcoustID: p.acoustIDLookup(),
		Committer: pushingCommitter{inner: p.importCommitter(), push: p.afterPlacing}, Provenance: p.acquisitionReleaseGroups, Logger: p.logger,
	})
}

// tvImporter is the TV module's batch importer: the same placement rules as music's commit, naming by the library's TV
// profile.
func (p *process) tvImporter() tv.Importer {
	categories := adapters.Categories{Cache: p.cache}
	service := p.tvService()
	return tv.NewImporter(tv.ImporterConfig{
		FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Physical: library.NewPhysical(p.library),
		Series: service.Series, Reader: library.NewSeriesReader(p.library), Identity: library.NewIdentity(p.library),
		Desired: library.NewDesired(p.library), Acquisitions: library.NewAcquisitions(p.library),
		Resolver:   p.tvResolver(),
		Categories: categories, BlockedContent: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		TransferLadder: p.transferLadder, RenameOnImport: p.renameOnImport, GroupID: lookupGroupID, BelowCutoff: p.belowCutoff,
		ExtraFiles: p.extraFileExtensions, SharedFiles: p.enqueueSharedFiles, AfterPlacing: p.enqueueServerFilesFor, Probe: p.probeMeasured,
		Fingerprint: p.fingerprintVideo, SameVideos: p.sameVideos,
		DuplicateCheck: func() bool { return p.cache.Snapshot().ImportDuplicateCheck() },
	})
}

// movieImporter is the movie module's batch importer, placing through the same steps as TV's.
func (p *process) movieImporter() movie.Importer {
	categories := adapters.Categories{Cache: p.cache}
	identity := library.NewIdentity(p.library)
	return movie.NewImporter(movie.ImporterConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Series: library.NewSeries(p.library),
		Reader: library.NewSeriesReader(p.library), Identity: identity, Desired: library.NewDesired(p.library),
		Acquisitions: library.NewAcquisitions(p.library),
		Placer: placer.New(placer.Config{
			FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Identity: identity,
			Blocked: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			TransferLadder: p.transferLadder, GroupID: lookupGroupID,
		}),
		Resolver:   p.movieResolver(),
		Categories: categories, Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		RenameOnImport: p.renameOnImport, ExtraFiles: p.extraFileExtensions, Probe: p.probeMeasured,
		AfterPlacing: p.enqueueMovieServerFilesFor,
	})
}

// belowCutoff says a measured episode file misses its library's cutoff.
func (p *process) belowCutoff(categoryID int64, file library.EpisodeFile) bool {
	snapshot := p.cache.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found || file.VideoCodec == "" {
		return false
	}
	judged, ok := quality.Judge(snapshot, category, tv.FileFacts(file))
	return ok && !judged.MeetsCutoff
}

// probeMeasured is what ffprobe measured of one file, for the import rules (sample by runtime, no audio track).
func (p *process) probeMeasured(ctx context.Context, absolutePath string) (mediatool.Measured, bool) {
	probes, err := p.tool.ReadTags(ctx, []string{absolutePath})
	if err != nil || len(probes) != 1 || probes[0].Err != nil {
		return mediatool.Measured{}, false
	}
	return probes[0].Measured, true
}

// fingerprintVideo hashes a downloaded video's pictures and reads its length, for the import's duplicate check.
func (p *process) fingerprintVideo(ctx context.Context, absolutePath string) (mediatool.VideoHash, int64, bool) {
	hash, err := mediatool.New(nil, 0).WithHardwareDecode(p.frameDecodeArgs).PerceptualHash(ctx, absolutePath)
	if err != nil {
		return mediatool.VideoHash{}, 0, false
	}
	measured, _ := p.probeMeasured(ctx, absolutePath)
	return hash, measured.Duration.Milliseconds(), true
}

// similarVideos compares by the tolerance setting as it stands at each comparison.
func (p *process) similarVideos() library.SimilarVideos {
	return library.NewSimilarVideos(p.library).WithFrameBits(func() int { return p.cache.Snapshot().VideoPhashFrameBits() })
}

// sameVideos are the files of a series that show the same pictures as a hash, at the finder's default level.
func (p *process) sameVideos(ctx context.Context, workID int64, hash mediatool.VideoHash, durationMs int64) ([]int64, error) {
	frames := make([]library.FrameHash, len(hash.Frames))
	for k, frame := range hash.Frames {
		frames[k] = library.FrameHash{Slot: frame.Slot, Hash: frame.Hash}
	}
	return p.similarVideos().MatchInWork(ctx, workID, hash.Mean, frames, durationMs, library.SimilarityMedium)
}

// adultImporter is the adult module's batch importer, placing through the same steps as a movie's.
func (p *process) adultImporter() adult.Importer {
	categories := adapters.Categories{Cache: p.cache}
	identity := library.NewIdentity(p.library)
	return adult.NewImporter(adult.ImporterConfig{
		FS: p.fsys, Physical: library.NewPhysical(p.library), Series: library.NewSeries(p.library),
		Reader: library.NewSeriesReader(p.library), Identity: identity, Desired: library.NewDesired(p.library),
		Acquisitions: library.NewAcquisitions(p.library),
		Placer: placer.New(placer.Config{
			FS: p.fsys, Roots: adapters.DrainRoots{Cache: p.cache}, Affinity: p.cache, Identity: identity,
			Blocked: library.NewBlockedContentStore(p.library), Clock: clock.NewSystem(), Logger: p.logger,
			TransferLadder: p.transferLadder, GroupID: lookupGroupID,
		}),
		Resolver:   p.adultResolver(),
		Categories: categories, Clock: clock.NewSystem(), Logger: p.logger,
		Exclusions:     adapters.WorkExclusions{Exclusions: library.NewExclusions(p.library)},
		RenameOnImport: p.renameOnImport, ExtraFiles: p.extraFileExtensions, Probe: p.probeMeasured, AfterPlacing: p.afterPlacing,
	})
}

// recycleSourceArchive moves one archive file of an imported source into the recycle bin of the download folder that
// holds it (the source's parent when no download folder does), under its library's retention.
func (p *process) recycleSourceArchive(ctx context.Context, categoryID *int64, sourcePath, file string) error {
	root := path.Dir(sourcePath)
	if folders, err := p.cache.DownloadFolders(ctx); err == nil {
		for _, folder := range folders {
			if fsx.Within(folder.Path, file) {
				root = folder.Path
				break
			}
		}
	}
	var categoryIDs []int64
	if categoryID != nil {
		categoryIDs = []int64{*categoryID}
	}
	recycling := adapters.RecyclingFor(adapters.Categories{Cache: p.cache}, categoryIDs)
	_, err := library.NewRecycleBin(p.library, p.fsys, clock.NewSystem()).WithOnline(p.rootOnline).Put(ctx, library.PutRequest{
		RootPath: root, OriginalPath: file, Reason: library.ReasonImported, Retain: recycling.Retain,
	})
	return err
}

// pushingCommitter tells the media servers of each album the music committer placed.
type pushingCommitter struct {
	inner music.Committer
	push  func(ctx context.Context, categoryID, workID int64)
}

func (c pushingCommitter) CommitMatchedUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags music.Resolved,
	categoryID int64, matched *music.AlbumPlacement, ladder []string,
) (music.Placed, error) {
	placed, err := c.inner.CommitMatchedUnit(ctx, sourceRoot, unit, best, tags, categoryID, matched, ladder)
	if err == nil {
		c.push(ctx, categoryID, placed.WorkID)
	}
	return placed, err
}

func (c pushingCommitter) CommitUnverifiedUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, tags music.Resolved, categoryID int64, ladder []string,
) (music.Placed, error) {
	placed, err := c.inner.CommitUnverifiedUnit(ctx, sourceRoot, unit, tags, categoryID, ladder)
	if err == nil {
		c.push(ctx, categoryID, placed.WorkID)
	}
	return placed, err
}

func (c pushingCommitter) IdentifyUnverified(ctx context.Context, fromWorkID int64, best model.Identification, categoryID int64) (music.Placed, error) {
	placed, err := c.inner.IdentifyUnverified(ctx, fromWorkID, best, categoryID)
	if err == nil {
		c.push(ctx, categoryID, placed.WorkID)
	}
	return placed, err
}
