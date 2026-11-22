package music

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/moving/mover"
	"github.com/makearr/Solarr/internal/moving/placement"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// PlacementRoots is the narrow settings-side view placement needs (ADR-25), without accepts_new or drain_state.
type PlacementRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
	CategoryRoots() []settings.CategoryRoot
}

// PlacementAffinity reads and writes placement_affinity (section 11.7); *settings.Cache satisfies it directly.
type PlacementAffinity interface {
	PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error)
	SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error
}

// Committer is ADR-48's COMMIT: place each decided file, register it, resolve its work/edition and attach it to uncovered parts; it trusts the Identification given.
type Committer struct {
	fsys             fsx.FS
	roots            PlacementRoots
	affinity         PlacementAffinity
	physical         library.Physical
	identity         library.Identity
	desired          *library.Desired
	categories       Categories
	blockedContent   BlockedContent
	clock            clock.Clock
	defaultProfileID int64
	transferLadder   TransferLadder
	renameOnImport   RenameOnImport
	extraFiles       ExtraFiles
	groupID          GroupID
	people           *library.People
	logger           *slog.Logger
}

// TransferLadder answers the ordered transfer methods, read per commit so a settings change needs no restart; nil or empty means mover.DefaultLadder.
type TransferLadder func() []mover.PlaceMethod

// RenameOnImport answers whether a committed file is named by the template
// or keeps the name it arrived with, read per commit for the same reason
// TransferLadder is. A nil func means renaming, the shipped default.
type RenameOnImport func() bool

// ExtraFiles answers the extensions to bring along with the tracks into a library (Lidarr's "Import Extra Files"),
// read per commit; nil or empty means none.
type ExtraFiles func(categoryID int64) []string

// GroupID resolves a group name for a library's "chown group"; nil accepts only numbers.
type GroupID func(name string) (int, error)

// BlockedContent is the ADR-53 part 2 check, narrowed to an interface so tests can fake it.
type BlockedContent interface {
	FindByHash(ctx context.Context, oshash, quickHash string, now int64) ([]library.BlockedContent, error)
}

// CommitterConfig is what a Committer needs. Every field is required except
// DefaultProfileID, which follows DiscoverWanted's own "used only when the
// category has no quality profile of its own configured yet" (todo 4.3's gap).
type CommitterConfig struct {
	FS               fsx.FS
	Roots            PlacementRoots
	Affinity         PlacementAffinity
	Physical         library.Physical
	Identity         library.Identity
	Desired          *library.Desired
	Categories       Categories
	BlockedContent   BlockedContent
	Clock            clock.Clock
	DefaultProfileID int64
	TransferLadder   TransferLadder
	RenameOnImport   RenameOnImport
	ExtraFiles       ExtraFiles
	GroupID          GroupID
	// People credits an unverified album's artist (D6); optional.
	People *library.People
	// Logger says what was kept despite a failure; optional.
	Logger *slog.Logger
}

// NewCommitter builds one.
func NewCommitter(config CommitterConfig) Committer {
	logger := config.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return Committer{
		fsys: config.FS, roots: config.Roots, affinity: config.Affinity, physical: config.Physical,
		identity: config.Identity, desired: config.Desired, categories: config.Categories,
		blockedContent: config.BlockedContent, clock: config.Clock,
		defaultProfileID: config.DefaultProfileID, transferLadder: config.TransferLadder,
		renameOnImport: config.RenameOnImport, extraFiles: config.ExtraFiles, groupID: config.GroupID, people: config.People,
		logger: logger,
	}
}

// Placed is what CommitUnit resolved and placed, so a revert knows exactly which rows it owns (ADR-48).
type Placed struct {
	WorkID         int64
	WorkCreated    bool
	EditionID      *int64
	EditionCreated bool
	MediaObjectIDs []int64
	// LibraryPathIDs parallels MediaObjectIDs: the path this unit created, since quick-hash dedupe may share an object, and a revert must undo only its own path.
	LibraryPathIDs []int64
	// PathWarnings are naming's non-fatal notices keyed by MediaObjectID; COMMIT has no preview step, so they are only logged.
	PathWarnings map[int64][]string
	// TransferMethods parallels MediaObjectIDs: which rung placed each file, recorded because a reflink and a copy look identical on disk.
	TransferMethods []mover.PlaceMethod
	// RootFolderID is where the unit was placed; Extras are paths under it.
	RootFolderID int64
	// Extras are the extra files placed beside the tracks, relative to the root folder.
	Extras []string
	// ExtrasSkipped explains each extra file that was not placed, for the log.
	ExtrasSkipped []string
}

// TransferMethodNames answers TransferMethods as plain strings, so a caller
// can persist them without taking a dependency on the mover for a conversion.
func (p Placed) TransferMethodNames() []string {
	if len(p.TransferMethods) == 0 {
		return nil
	}
	names := make([]string, len(p.TransferMethods))
	for index, method := range p.TransferMethods {
		names[index] = string(method)
	}
	return names
}

// ErrNoPlacement is no root folder in the category qualifies, so the unit stays decided but unplaced for a retry (section 11.7).
var ErrNoPlacement = errors.New("music: no root folder available to place this unit")

// ErrBlockedContent is a quick hash matched blocked_content (ADR-53); the message starts with model.ReasonBlockedContent so callers can record the reason.
var ErrBlockedContent = errors.New(model.ReasonBlockedContent)

// errNoExternalID is a genuine caller error (no current identifier produces it); refuse loudly rather than key a work on an empty id pair.
var errNoExternalID = errors.New("music: identification has no external id to resolve a work by")

// AlbumPlacement is what album matching decided for a unit: the release it matched, how many
// discs that release spans, and which track each file is, keyed by the file's own RelPath.
type AlbumPlacement struct {
	ReleaseMBID string
	// AlbumTitle and ArtistName are the release's as MusicBrainz spells them, what enrichment names the album by.
	AlbumTitle string
	ArtistName string
	DiscCount  int
	Tracks     map[string]ReleaseTrack
}

// PlacementOf is the album decision as a unit's placement; nil unless an album was identified.
func PlacementOf(decision AlbumDecision, relPaths []string) *AlbumPlacement {
	if decision.Best == nil {
		return nil
	}
	placement := &AlbumPlacement{
		ReleaseMBID: decision.Best.Candidate.Release.MBID,
		AlbumTitle:  decision.Best.Candidate.Release.Title,
		ArtistName:  decision.Best.Candidate.ArtistCredit,
		DiscCount:   len(decision.Best.Candidate.Release.Media),
		Tracks:      make(map[string]ReleaseTrack, len(relPaths)),
	}
	for index, relPath := range relPaths {
		if index < len(decision.Best.Tracks) && decision.Best.Tracks[index] != nil {
			placement.Tracks[relPath] = *decision.Best.Tracks[index]
		}
	}
	return placement
}

// trackOf is the file as album matching placed it, or as its own tags claim when it matched no track.
func (p *AlbumPlacement) trackOf(relPath string, tags mediatool.Tags) (TrackFile, int) {
	claimed := ReadTrackFile(0, "", tags)
	if claimed.Title == "" {
		// Untagged files would all be named alike and collide.
		claimed.Title = strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	}
	if p == nil {
		return claimed, 0
	}
	matched, found := p.Tracks[relPath]
	if !found {
		return claimed, p.DiscCount
	}
	claimed.Disc, claimed.Track, claimed.RecordingMBID = matched.Disc, matched.Position, matched.RecordingMBID
	if matched.Title != "" {
		claimed.Title = matched.Title
	}
	return claimed, p.DiscCount
}

// CommitUnit places unit (found under sourceRoot by inspect.Run) into categoryID's own
// library, from each file's own tags. best is the unit's DECIDE-cleared Identification;
// tags is the Resolved batch IDENTIFY already probed (ADR-48's own speed rule).
func (c Committer) CommitUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags Resolved, categoryID int64,
) (Placed, error) {
	return c.CommitMatchedUnit(ctx, sourceRoot, unit, best, tags, categoryID, nil, nil)
}

// CommitMatchedUnit is CommitUnit knowing what album matching decided: the release to record as
// the edition, and which track each file is (placement nil falls back to each file's own tags).
// ladder overrides the configured transfer order for this unit; nil keeps it.
func (c Committer) CommitMatchedUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags Resolved,
	categoryID int64, matched *AlbumPlacement, ladder []string,
) (Placed, error) {
	if best.ExternalID == nil {
		return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, errNoExternalID)
	}
	if len(unit.Media) == 0 {
		return Placed{}, fmt.Errorf("music: commit unit %q: no media files", unit.Directory)
	}

	var totalBytes int64
	for _, f := range unit.Media {
		totalBytes += f.SizeBytes
	}

	// ADR-53 part 2 gate: hash and check every file from the source before placing any, so a block on file 2 never leaves file 1 copied; the hashes are reused after placement.
	quickHashes := make([]string, len(unit.Media))
	for index, media := range unit.Media {
		hash, err := library.QuickHash(c.fsys, path.Join(sourceRoot, media.RelPath), media.SizeBytes)
		if err != nil {
			return Placed{}, fmt.Errorf("music: quick hash %q: %w", media.RelPath, err)
		}
		quickHashes[index] = hash
	}
	if c.blockedContent != nil {
		now := c.clock.Now().Unix()
		for index, hash := range quickHashes {
			blocks, err := c.blockedContent.FindByHash(ctx, "", hash, now)
			if err != nil {
				return Placed{}, fmt.Errorf("music: check blocked content for unit %q: %w", unit.Directory, err)
			}
			if len(blocks) > 0 {
				return Placed{}, fmt.Errorf("%w: %s matched %s", ErrBlockedContent, blocks[0].Reason, unit.Media[index].RelPath)
			}
		}
	}

	representative, err := tags.TagsFor(ctx, model.FileRef{RelPath: unit.Media[0].RelPath})
	if err != nil {
		return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
	}
	work := unitWorkIdentity(sourceRoot, unit, representative)
	edition := unitEditionLink(representative)
	if matched != nil && matched.ReleaseMBID != "" {
		// Album matching already chose the release; keeping it saves enrichment choosing a
		// second time from tags alone, which picked a 15-track edition for 17 matched files.
		edition = releaseEditionLink(matched.ReleaseMBID, best.Confidence)
	}
	artist := representative.First("albumartist", "album_artist", "artist")
	if artist == "" {
		// Without it the path lost its artist folder and the files began "- 01" (found 2026-09-30).
		artist, _ = folderNames(sourceRoot, unit.Directory)
	}
	year := parseYear(representative.First("date", "year"))
	known, found, err := c.identity.KnownWork(ctx, best.ExternalID.Provider, best.ExternalID.ExternalID)
	if err != nil {
		return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
	}
	if found {
		// Tags said "HEKA" where MusicBrainz says "Heka": a known album is named canonically.
		work.CanonicalTitle, work.SortTitle = known.Title, library.SortTitle(known.Title)
		if known.ArtistName != "" {
			artist = known.ArtistName
		}
		if year == nil {
			year = known.Year
		}
	} else if matched != nil && matched.AlbumTitle != "" {
		// A new album is named as enrichment will name it, or every import showed up as a rename at once.
		work.CanonicalTitle, work.SortTitle = matched.AlbumTitle, library.SortTitle(matched.AlbumTitle)
		if matched.ArtistName != "" {
			artist = matched.ArtistName
		}
	}

	affinityKey := artistAffinityKey(artist)
	var existingAffinityRoot *int64
	if affinityKey != "" {
		if rootFolderID, found, err := c.affinity.PlacementAffinity(ctx, categoryID, affinityKey); err != nil {
			return Placed{}, fmt.Errorf("music: read placement affinity for unit %q: %w", unit.Directory, err)
		} else if found {
			existingAffinityRoot = &rootFolderID
		}
	}

	root, decision, err := placement.Resolve(placement.Request{
		CategoryID: categoryID, SizeBytes: totalBytes, Strategy: placement.StrategyMostFreeSpace,
		AffinityEnabled: affinityKey != "", AffinityKey: affinityKey, ExistingAffinityRoot: existingAffinityRoot,
	}, c.rootCandidates(categoryID))
	if err != nil {
		return Placed{}, fmt.Errorf("music: resolve placement for unit %q: %w", unit.Directory, err)
	}
	if decision.Outcome != placement.OutcomePlaced {
		return Placed{}, fmt.Errorf("%w: %s", ErrNoPlacement, decision.Reason)
	}
	destRoot, ok := c.roots.RootFolder(root.RootFolderID)
	if !ok {
		return Placed{}, fmt.Errorf("%w: unit %q: root folder %d has no known path", ErrNoPlacement, unit.Directory, root.RootFolderID)
	}

	now := c.clock.Now()
	if affinityKey != "" {
		if err := c.affinity.SetPlacementAffinity(ctx, categoryID, affinityKey, root.RootFolderID, now.Unix()); err != nil {
			return Placed{}, fmt.Errorf("music: record placement affinity for unit %q: %w", unit.Directory, err)
		}
	}

	methods := mover.DefaultLadder
	if c.transferLadder != nil {
		if configured := c.transferLadder(); len(configured) > 0 {
			methods = configured
		}
	}
	if len(ladder) > 0 {
		methods = make([]mover.PlaceMethod, len(ladder))
		for i, method := range ladder {
			methods[i] = mover.PlaceMethod(method)
		}
	}

	names := NamingFor(nil)
	fileManagement := settings.DefaultFileManagement
	if category, found := c.categories.Category(categoryID); found {
		names = NamingFor(category.Naming)
		fileManagement = category.FileManagement
	}
	// The Import screen's "rename" checkbox can only turn renaming off, never override the library.
	if c.renameOnImport != nil && !c.renameOnImport() {
		names.RenameFiles = false
	}

	destRelPaths := make([]string, len(unit.Media))
	files := make([]library.PhysicalFile, len(unit.Media))
	trackFiles := make([]TrackFile, len(unit.Media))
	pathWarnings := make([][]string, len(unit.Media))
	transferMethods := make([]mover.PlaceMethod, len(unit.Media))
	var placedSoFar []placedFile
	var registered []library.RegisteredFile
	// A unit that fails part-way takes back what it placed, rather than leaving files nothing tracks.
	undo := func(err error) (Placed, error) {
		return Placed{}, errors.Join(err, c.undoPlaced(placedSoFar), c.physical.Unregister(context.WithoutCancel(ctx), registered))
	}
	// Unmatched, the files' own disc tags are all there is: two discs named as one would collide on their track numbers.
	claimedDiscs := 0
	if matched == nil {
		for _, media := range unit.Media {
			if fileTags, err := tags.TagsFor(ctx, model.FileRef{RelPath: media.RelPath}); err == nil {
				claimedDiscs = max(claimedDiscs, ReadTrackFile(0, "", fileTags).Disc)
			}
		}
	}
	for index, media := range unit.Media {
		fileTags, err := tags.TagsFor(ctx, model.FileRef{RelPath: media.RelPath})
		if err != nil {
			return undo(fmt.Errorf("music: commit unit %q: %w", unit.Directory, err))
		}
		track, discCount := matched.trackOf(media.RelPath, fileTags)
		if matched == nil && claimedDiscs > 1 {
			discCount = claimedDiscs
		}
		destRelPath, warnings, err := importRelPath(names, work, artist, year, track, discCount, media.RelPath)
		if err != nil {
			return undo(fmt.Errorf("music: commit unit %q: %w", unit.Directory, err))
		}
		pathWarnings[index] = warnings

		sourceAbs := path.Join(sourceRoot, media.RelPath)
		destAbs := path.Join(destRoot.Path, destRelPath)
		method, err := mover.PlaceOneOnDisk(ctx, c.fsys, sourceAbs, destAbs, media.SizeBytes, methods)
		if err != nil {
			return undo(fmt.Errorf("music: place %q: %w", media.RelPath, err))
		}
		placedSoFar = append(placedSoFar, placedFile{source: sourceAbs, dest: destAbs, sizeBytes: media.SizeBytes, method: method})
		if method != mover.PlaceSymlink {
			// A symlink's bytes are the source's; a hardlink's are shared, as in Sonarr.
			pathWarnings[index] = append(pathWarnings[index], c.finishFile(destRoot.Path, destAbs, fileManagement, fileTags)...)
		}
		transferMethods[index] = method
		destRelPaths[index] = destRelPath
		files[index] = library.PhysicalFile{
			RelPath: destRelPath, SizeBytes: media.SizeBytes, QuickHash: quickHashes[index], FirstSeenAt: now,
		}
		trackFiles[index] = track
	}

	registered, err = c.physical.RegisterBatch(ctx, destRoot.ID, files)
	if err != nil {
		return undo(fmt.Errorf("music: register placed files for unit %q: %w", unit.Directory, err))
	}
	mediaObjectIDs := make([]int64, len(registered))
	libraryPathIDs := make([]int64, len(registered))
	var pathWarningsByObject map[int64][]string
	for index, file := range registered {
		mediaObjectIDs[index] = file.MediaObjectID
		libraryPathIDs[index] = file.LibraryPathID
		trackFiles[index].MediaObjectID = file.MediaObjectID
		if len(pathWarnings[index]) > 0 {
			if pathWarningsByObject == nil {
				pathWarningsByObject = make(map[int64][]string)
			}
			pathWarningsByObject[file.MediaObjectID] = pathWarnings[index]
		}
	}

	// One link per placed file, as a scan links them: a later rescan then knows every file is this
	// work's, even before its tracklist exists (found live 2026-09-29: 13 of 14 were left unlinked).
	links := make([]library.WorkLink, len(mediaObjectIDs))
	for index, mediaObjectID := range mediaObjectIDs {
		links[index] = library.WorkLink{
			Provider: best.ExternalID.Provider, ExternalID: best.ExternalID.ExternalID,
			Confidence: best.ExternalID.Confidence, Work: work, Edition: edition,
			RootFolderID: destRoot.ID, MediaObjectID: mediaObjectID,
		}
	}
	resolutions, err := c.identity.ResolveWorksDetailed(ctx, now, links)
	if err != nil {
		return undo(fmt.Errorf("music: resolve identity for unit %q: %w", unit.Directory, err))
	}
	resolution := resolutions[0]

	if err := DiscoverWanted(
		ctx, c.desired, c.categories, c.clock, c.defaultProfileID, categoryID, resolution.WorkID, trackFiles,
	); err != nil {
		// The files are placed and linked; the next scan finds the wanted parts again.
		c.logger.Warn("wanted parts of a placed album not discovered", slog.String("unit", unit.Directory), slog.Any("error", err))
	}

	var extensions []string
	if c.extraFiles != nil {
		extensions = c.extraFiles(categoryID)
	}
	extras, extrasSkipped := c.placeExtras(ctx, sourceRoot, destRoot.Path, unit, destRelPaths, extensions, methods)

	return Placed{
		WorkID: resolution.WorkID, WorkCreated: resolution.WorkCreated,
		EditionID: resolution.EditionID, EditionCreated: resolution.EditionCreated,
		MediaObjectIDs: mediaObjectIDs, LibraryPathIDs: libraryPathIDs,
		PathWarnings: pathWarningsByObject, TransferMethods: transferMethods,
		RootFolderID: destRoot.ID, Extras: extras, ExtrasSkipped: extrasSkipped,
	}, nil
}

// placeExtras brings unit.Other files with a listed extension along: one named like a track
// follows that track's new name (Lidarr's rule), any other goes to the folder of the tracks it
// sat beside. An existing file is never overwritten, and a failure skips the file, not the album.
func (c Committer) placeExtras(
	ctx context.Context, sourceRoot, destRootPath string, unit inspect.Unit, destRelPaths, extensions []string, methods []mover.PlaceMethod,
) (placed, skipped []string) {
	if len(extensions) == 0 {
		return nil, nil
	}
	wanted := make(map[string]bool, len(extensions))
	for _, extension := range extensions {
		wanted["."+extension] = true
	}
	trackByStem := make(map[string]string, len(unit.Media))
	folderBySourceDir := make(map[string]string, len(unit.Media))
	for index, media := range unit.Media {
		trackByStem[pathStem(media.RelPath)] = destRelPaths[index]
		if _, found := folderBySourceDir[path.Dir(media.RelPath)]; !found {
			folderBySourceDir[path.Dir(media.RelPath)] = path.Dir(destRelPaths[index])
		}
	}

	claimed := make(map[string]bool)
	for _, other := range unit.Other {
		extension := strings.ToLower(path.Ext(other.RelPath))
		if !wanted[extension] {
			continue
		}
		var destRelPath string
		if track, found := trackByStem[pathStem(other.RelPath)]; found {
			destRelPath = strings.TrimSuffix(track, path.Ext(track)) + extension
		} else {
			folder, found := folderBySourceDir[path.Dir(other.RelPath)]
			if !found {
				folder = path.Dir(destRelPaths[0])
			}
			destRelPath = path.Join(folder, fsx.SanitiseComponent(path.Base(other.RelPath)))
		}
		destAbs := path.Join(destRootPath, destRelPath)
		if _, err := c.fsys.Stat(destAbs); err == nil || claimed[destRelPath] {
			skipped = append(skipped, other.RelPath+": "+destRelPath+" already exists")
			continue
		}
		if _, err := mover.PlaceOneOnDisk(ctx, c.fsys, path.Join(sourceRoot, other.RelPath), destAbs, other.SizeBytes, methods); err != nil {
			skipped = append(skipped, other.RelPath+": "+err.Error())
			continue
		}
		claimed[destRelPath] = true
		placed = append(placed, destRelPath)
	}
	return placed, skipped
}

// placedFile is one file a unit placed, enough to take it back.
type placedFile struct {
	source, dest string
	sizeBytes    int64
	method       mover.PlaceMethod
}

// undoPlaced takes back a failed unit's files: a moved file goes back to its source, since it is the
// only copy; anything else is removed from the library. Answers what could not be taken back.
func (c Committer) undoPlaced(placed []placedFile) error {
	var failures []error
	for _, file := range placed {
		var err error
		if file.method == mover.PlaceMove {
			// A take-back must finish, so it does not follow the caller's cancel.
			_, err = mover.PlaceOne(context.Background(), c.fsys, file.dest, file.source, file.sizeBytes, []mover.PlaceMethod{mover.PlaceMove})
		} else {
			err = c.fsys.Remove(file.dest)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("music: could not take back %q: %w", file.dest, err))
		}
	}
	return errors.Join(failures...)
}

// finishFile applies the library's Change File Date and Set Permissions to one placed track.
// Neither is worth failing an import over, so a failure comes back as a warning.
func (c Committer) finishFile(
	rootPath, fileAbs string, fileManagement settings.FileManagement, tags mediatool.Tags,
) []string {
	var warnings []string
	if fileManagement.ChangeFileDate == settings.FileDateReleaseDate {
		if released, ok := parseReleaseDate(tags.First("date", "originaldate", "year")); ok {
			if err := c.fsys.Chtimes(fileAbs, released); err != nil {
				warnings = append(warnings, "file date not changed: "+err.Error())
			}
		}
	}
	if fileManagement.SetPermissions {
		permissions, err := c.permissions(fileManagement)
		if err == nil {
			err = mover.ApplyPermissions(c.fsys, rootPath, fileAbs, permissions)
		}
		if err != nil {
			warnings = append(warnings, "permissions not set: "+err.Error())
		}
	}
	return warnings
}

func (c Committer) permissions(fileManagement settings.FileManagement) (mover.Permissions, error) {
	return PermissionsFor(fileManagement, c.groupID)
}

// PermissionsFor turns a library's settings into modes and a group id, -1 when no group is set.
func PermissionsFor(fileManagement settings.FileManagement, groupID GroupID) (mover.Permissions, error) {
	return mover.PermissionsOf(fileManagement.FolderMode(), fileManagement.FileMode(), fileManagement.ChownGroup, groupID)
}

// parseReleaseDate reads a tag's 1997, 1997-06 or 1997-06-16 as midnight UTC on that day.
func parseReleaseDate(value string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if len(value) >= len(layout) {
			if parsed, err := time.Parse(layout, value[:len(layout)]); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// pathStem is a path without its extension, compared case-insensitively.
func pathStem(relPath string) string {
	return strings.ToLower(strings.TrimSuffix(relPath, path.Ext(relPath)))
}

// rootCandidates converts the category's roots to placement form; duplicated from drainjob because the two packages must not import each other.
func (c Committer) rootCandidates(categoryID int64) []placement.RootCandidate {
	return rootCandidates(c.roots, categoryID)
}

func rootCandidates(roots PlacementRoots, categoryID int64) []placement.RootCandidate {
	acceptsNew := map[int64]bool{}
	var order []int64
	seen := map[int64]bool{}
	for _, attachment := range roots.CategoryRoots() {
		if attachment.CategoryID != categoryID {
			continue
		}
		if !seen[attachment.RootFolderID] {
			seen[attachment.RootFolderID] = true
			order = append(order, attachment.RootFolderID)
		}
		if attachment.AcceptsNew {
			acceptsNew[attachment.RootFolderID] = true
		}
	}

	candidates := make([]placement.RootCandidate, 0, len(order))
	for _, id := range order {
		root, ok := roots.RootFolder(id)
		if !ok {
			continue
		}
		candidates = append(candidates, placement.CandidateOf(root, acceptsNew[id]))
	}
	return candidates
}

// artistAffinityKey builds section 11.7's lowercased, trimmed artist key (same as pack grouping); empty means affinity does not apply.
func artistAffinityKey(artist string) string {
	key := strings.ToLower(strings.TrimSpace(artist))
	if key == "" {
		return ""
	}
	return "artist:" + key
}

// unitWorkIdentity names the work to create, with newWorkIdentity's tag-first, directory-fallback rule read from the unit's first file.
func unitWorkIdentity(sourceRoot string, unit inspect.Unit, tags mediatool.Tags) library.WorkIdentity {
	title := tags.First("album")
	if title == "" {
		_, title = folderNames(sourceRoot, unit.Directory)
	}
	return library.WorkIdentity{MediaType: "music", CanonicalTitle: title, SortTitle: library.SortTitle(title)}
}

// releaseEditionLink is the placeholder edition for a release album matching chose, the same
// shape unitEditionLink builds from a tag (enrichment refines it into the real edition).
func releaseEditionLink(releaseMBID string, confidence float64) *library.EditionLink {
	return &library.EditionLink{
		Provider: ProviderRelease, ExternalID: releaseMBID, Confidence: confidence,
		Edition: library.EditionIdentity{Kind: "unknown", Label: releaseMBID},
	}
}

// unitEditionLink is newEditionLink, read from a unit's representative tags
// instead of one scan.Observed.
func unitEditionLink(tags mediatool.Tags) *library.EditionLink {
	release := ReadMBIDs(tags).Release
	if release == "" {
		return nil
	}
	return &library.EditionLink{
		Provider: ProviderRelease, ExternalID: release, Confidence: 1.0,
		Edition: library.EditionIdentity{Kind: "unknown", Label: release},
	}
}

// importRelPath renders one file's destination: MusicBrainz's names where album matching found the release, else tags.
func importRelPath(
	names Naming, work library.WorkIdentity, artist string, year *int64, track TrackFile, discCount int,
	sourceRelPath string,
) (string, []string, error) {
	facts := TrackNaming{
		ArtistName: artist, AlbumTitle: work.CanonicalTitle, Medium: track.Disc, Track: track.Track,
		TrackTitle:       track.Title,
		OriginalFilename: strings.TrimSuffix(path.Base(sourceRelPath), path.Ext(sourceRelPath)),
	}
	if year != nil {
		facts.ReleaseYear = int(*year)
	}
	rendered, warnings, err := names.RenderTrack(facts, discCount, sourceRelPath)
	if err != nil {
		return "", nil, fmt.Errorf("render path: %w", err)
	}
	var messages []string
	for _, w := range warnings {
		messages = append(messages, w.Message)
	}
	return rendered, messages, nil
}

// keptFileName is rename-on-import off: the template picks folders, the file keeps its arrived name, sanitised for the destination.
func keptFileName(renderedRelPath, sourceRelPath string) string {
	kept := fsx.SanitiseComponent(strings.TrimSpace(path.Base(sourceRelPath)))
	if kept == "" {
		return renderedRelPath
	}
	return path.Join(path.Dir(renderedRelPath), kept)
}
