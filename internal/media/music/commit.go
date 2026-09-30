package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
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

// PlacementRoots is the settings-side facts CommitUnit's placement step
// needs -- the same narrow shape internal/moving/drainjob.Roots already
// keeps from a full settings.Cache (ADR-25: this package never imports the
// cache itself), scoped down further since import never touches
// accepts_new or drain_state.
type PlacementRoots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
	CategoryRoots() []settings.CategoryRoot
}

// PlacementAffinity is the settings-side placement_affinity read/write
// CommitUnit's artist-affinity step needs (section 11.7: "affinity keeps an
// artist or series on one disk once placed" -- a general placement rule, not
// pack-specific). *settings.Cache already satisfies this directly --
// PlacementAffinity/SetPlacementAffinity are its own methods, not routed
// through a Snapshot the way PlacementRoots's fields are -- so callers can
// wire the cache straight through with no adapter needed.
type PlacementAffinity interface {
	PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error)
	SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error
}

// Committer is ADR-48's own "COMMIT actually imports" (todo 4.9): place
// every file a unit's IDENTIFY/DECIDE steps already decided to commit
// (mover.PlaceOne, ADR-11 A -- reflink/hardlink/copy, never the source
// touched), register it (library.Physical, the same call Consumer.Observe
// already makes for a scanned file), resolve its work/edition
// (library.Identity.ResolveWorksDetailed) and attach whatever of its files
// fill the album's still-uncovered parts (DiscoverWanted, shared with
// Consumer's own scan-time discovery). Everything here happens after
// IDENTIFY already ran during WALK/GROUP -- CommitUnit trusts the
// Identification it is given rather than re-running the chain.
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
}

// TransferLadder answers the ordered transfer methods import should try, read
// per commit rather than captured once so a settings change takes effect
// without a restart -- the same live-read shape the egress proxy already uses.
// A nil func or an empty result means mover.DefaultLadder.
type TransferLadder func() []mover.PlaceMethod

// RenameOnImport answers whether a committed file is named by the template
// or keeps the name it arrived with, read per commit for the same reason
// TransferLadder is. A nil func means renaming, the shipped default.
type RenameOnImport func() bool

// ExtraFiles answers the extensions to bring along with the tracks (Lidarr's "Import Extra
// Files"), read per commit; nil or empty means none.
type ExtraFiles func() []string

// GroupID resolves a group name for a library's "chown group"; nil accepts only numbers.
type GroupID func(name string) (int, error)

// BlockedContent is the ADR-53 part 2 check CommitUnit's import gate needs
// (todo 4.15) -- library.BlockedContentStore's own shape, narrowed to an
// interface so a test can fake it without a real library.db the way
// Committer already does for Physical/Identity's own real dependencies.
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
}

// NewCommitter builds one.
func NewCommitter(config CommitterConfig) Committer {
	return Committer{
		fsys: config.FS, roots: config.Roots, affinity: config.Affinity, physical: config.Physical,
		identity: config.Identity, desired: config.Desired, categories: config.Categories,
		blockedContent: config.BlockedContent, clock: config.Clock,
		defaultProfileID: config.DefaultProfileID, transferLadder: config.TransferLadder,
		renameOnImport: config.RenameOnImport, extraFiles: config.ExtraFiles, groupID: config.GroupID, people: config.People,
	}
}

// Placed is what CommitUnit resolved and placed -- everything import_unit
// and import_unit_file need to record, for display and for a later
// session revert to know precisely which rows it is responsible for
// (ADR-48: revert "removes the rows the session created", never one it
// only matched).
type Placed struct {
	WorkID         int64
	WorkCreated    bool
	EditionID      *int64
	EditionCreated bool
	MediaObjectIDs []int64
	// LibraryPathIDs is parallel to MediaObjectIDs: which library_path row this
	// unit's own placement created for each file, since RegisterBatch's ADR-11
	// quick-hash dedupe can point a MediaObjectID at bytes another, unrelated
	// commit already owns -- a revert must undo only its own path, never a path
	// it merely shares an object with (4.22's own linking audit).
	LibraryPathIDs []int64
	// PathWarnings is naming.RenderPath's own non-fatal notices (today: only
	// the total-length recommendation), keyed by MediaObjectID -- COMMIT runs
	// inline with no separate preview step to block on, so the caller's own
	// choice is what to do with these (todo 4.9: logged, not surfaced in the
	// UI, since nothing about import_unit persists a per-file warning today).
	PathWarnings map[int64][]string
	// TransferMethods is parallel to MediaObjectIDs: which rung of the ladder
	// actually placed each file. Recorded because no two rungs are
	// distinguishable on disk -- a reflink and a copy look identical, which is
	// how an XFS import read as "it copied everything" (2026-09-18).
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

// ErrNoPlacement means placement.Resolve deferred: no root folder in
// categoryID currently qualifies. The unit stays COMMIT-decided-but-unplaced
// (import_unit.work_id still NULL) for a later retry, the same as any other
// DEFER (section 11.7).
var ErrNoPlacement = errors.New("music: no root folder available to place this unit")

// ErrBlockedContent means one of unit.Media's own quick hashes matched
// blocked_content (ADR-53 part 2, todo 4.15's import gate): the message
// itself carries model.ReasonBlockedContent as a matchable prefix, so a
// caller distinguishing this from ErrNoPlacement can also record the
// block's own reason directly, without re-deriving it.
var ErrBlockedContent = errors.New(model.ReasonBlockedContent)

// errNoExternalID means best carries no ExternalID at all -- today's own
// identifiers (MBIDIdentifier, SearchIdentifier) never produce that (see
// consumer.go's identical comment on identifyBatch), so this is a genuine
// caller error, not a real-world case, and CommitUnit refuses loudly rather
// than creating a work keyed on an empty provider/external id pair.
var errNoExternalID = errors.New("music: identification has no external id to resolve a work by")

// AlbumPlacement is what album matching decided for a unit: the release it matched, how many
// discs that release spans, and which track each file is, keyed by the file's own RelPath.
type AlbumPlacement struct {
	ReleaseMBID string
	DiscCount   int
	Tracks      map[string]ReleaseTrack
}

// PlacementOf is the album decision as a unit's placement; nil unless an album was identified.
func PlacementOf(decision AlbumDecision, relPaths []string) *AlbumPlacement {
	if decision.Best == nil {
		return nil
	}
	placement := &AlbumPlacement{
		ReleaseMBID: decision.Best.Candidate.Release.MBID,
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
	claimed := ReadTrackFile(0, tags)
	if p == nil {
		return claimed, 0
	}
	matched, found := p.Tracks[relPath]
	if !found {
		return claimed, p.DiscCount
	}
	claimed.Disc, claimed.Track, claimed.RecordingMBID = matched.Disc, matched.Position, matched.RecordingMBID
	if claimed.Title == "" {
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

	// ADR-53 part 2's own gate (todo 4.15): hashed from the source, before
	// any file reaches the library -- "nothing about a blocked file reaches
	// the library" only holds if the check runs before mover.PlaceOne, not
	// after. Every file's hash is computed and checked before any of them
	// are placed, so a block on file 2 of 3 never leaves file 1 already
	// copied in (ADR-53: "unit contains a refuse pattern -> whole unit
	// refused", the same all-or-nothing shape the exclusion-pattern gate
	// gives at DECIDE time). quickHashes is reused below instead of hashing
	// the destination a second time -- source and destination bytes are
	// identical after placement (ADR-11: reflink/hardlink/copy, never a
	// transform).
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
	work := unitWorkIdentity(unit, representative)
	edition := unitEditionLink(representative)
	if matched != nil && matched.ReleaseMBID != "" {
		// Album matching already chose the release; keeping it saves enrichment choosing a
		// second time from tags alone, which picked a 15-track edition for 17 matched files.
		edition = releaseEditionLink(matched.ReleaseMBID, best.Confidence)
	}
	artist := representative.First("albumartist", "album_artist", "artist")
	year := parseYear(representative.First("date", "year"))
	known, found, err := c.identity.KnownWork(ctx, best.ExternalID.Provider, best.ExternalID.ExternalID)
	if err != nil {
		return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
	}
	if found {
		// Tags said "HEKA" where MusicBrainz says "Heka": a known album is named canonically.
		work.CanonicalTitle, work.SortTitle = known.Title, SortTitle(known.Title)
		if known.ArtistName != "" {
			artist = known.ArtistName
		}
		if year == nil {
			year = known.Year
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
		return Placed{}, fmt.Errorf("music: commit unit %q: root folder %d has no known path", unit.Directory, root.RootFolderID)
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
	for index, media := range unit.Media {
		fileTags, err := tags.TagsFor(ctx, model.FileRef{RelPath: media.RelPath})
		if err != nil {
			return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
		}
		track, discCount := matched.trackOf(media.RelPath, fileTags)
		destRelPath, warnings, err := importRelPath(names, work, artist, year, track, discCount, media.RelPath)
		if err != nil {
			return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
		}
		pathWarnings[index] = warnings

		sourceAbs := path.Join(sourceRoot, media.RelPath)
		destAbs := path.Join(destRoot.Path, destRelPath)
		method, err := mover.PlaceOne(c.fsys, sourceAbs, destAbs, media.SizeBytes, methods)
		if err != nil {
			return Placed{}, fmt.Errorf("music: place %q: %w", media.RelPath, err)
		}
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

	registered, err := c.physical.RegisterBatch(ctx, destRoot.ID, files)
	if err != nil {
		return Placed{}, fmt.Errorf("music: register placed files for unit %q: %w", unit.Directory, err)
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
		return Placed{}, fmt.Errorf("music: resolve identity for unit %q: %w", unit.Directory, err)
	}
	resolution := resolutions[0]

	if err := DiscoverWanted(
		ctx, c.desired, c.categories, c.clock, c.defaultProfileID, categoryID, resolution.WorkID, trackFiles,
	); err != nil {
		return Placed{}, fmt.Errorf("music: discover wanted for unit %q: %w", unit.Directory, err)
	}

	var extensions []string
	if c.extraFiles != nil {
		extensions = c.extraFiles()
	}
	extras, extrasSkipped := c.placeExtras(sourceRoot, destRoot.Path, unit, destRelPaths, extensions, methods)

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
	sourceRoot, destRootPath string, unit inspect.Unit, destRelPaths, extensions []string, methods []mover.PlaceMethod,
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
		if _, err := mover.PlaceOne(c.fsys, path.Join(sourceRoot, other.RelPath), destAbs, other.SizeBytes, methods); err != nil {
			skipped = append(skipped, other.RelPath+": "+err.Error())
			continue
		}
		claimed[destRelPath] = true
		placed = append(placed, destRelPath)
	}
	return placed, skipped
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
	permissions := mover.Permissions{FolderMode: fileManagement.FolderMode(), FileMode: fileManagement.FileMode(), GID: -1}
	group := fileManagement.ChownGroup
	if group == "" {
		return permissions, nil
	}
	if gid, err := strconv.Atoi(group); err == nil {
		permissions.GID = gid
		return permissions, nil
	}
	if groupID == nil {
		return permissions, fmt.Errorf("group %q cannot be looked up here", group)
	}
	gid, err := groupID(group)
	if err != nil {
		return permissions, fmt.Errorf("group %q: %w", group, err)
	}
	permissions.GID = gid
	return permissions, nil
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

// rootCandidates answers every root folder attached to categoryID, in
// placement.RootCandidate form -- the same conversion
// internal/moving/drainjob.rootCandidate already does for drain, duplicated
// rather than shared since drainjob's own is unexported and the two
// packages must not import each other (both sit beside, not above, the
// other in architecture §2's graph).
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
		var freeBytes, totalBytes int64
		if root.FreeBytes != nil {
			freeBytes = *root.FreeBytes
		}
		if root.TotalBytes != nil {
			totalBytes = *root.TotalBytes
		}
		candidates = append(candidates, placement.RootCandidate{
			RootFolderID: root.ID, FSID: root.FSID, Online: root.Online, Writable: root.Writable,
			ReadOnly: root.ReadOnly, AcceptsNew: acceptsNew[id], FreeBytes: freeBytes, TotalBytes: totalBytes,
			MinFreeBytes: root.MinFreeBytes,
		})
	}
	return candidates
}

// artistAffinityKey builds section 11.7's artist-scoped placement affinity
// key from a unit's own tag-read artist name -- the same lowercased/trimmed
// comparison key internal/importing/pack's own sibling-artist grouping
// already uses, so a separator or casing difference never fragments one
// artist's affinity across two keys. An empty/unknown artist returns "",
// which the caller reads as "affinity does not apply to this unit" rather
// than colliding every unknown-artist unit onto one key.
func artistAffinityKey(artist string) string {
	key := strings.ToLower(strings.TrimSpace(artist))
	if key == "" {
		return ""
	}
	return "artist:" + key
}

// unitWorkIdentity names the work a unit resolves to if one has to be
// created -- the same tag-first, directory-fallback rule newWorkIdentity
// already applies to one scanned file, read here from the unit's own
// representative (first file's) tags instead.
func unitWorkIdentity(unit inspect.Unit, tags mediatool.Tags) library.WorkIdentity {
	title := tags.First("album")
	if title == "" {
		title = albumTitleFallback(unit.Directory)
	}
	return library.WorkIdentity{MediaType: "music", CanonicalTitle: title, SortTitle: SortTitle(title)}
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

// importRelPath renders one file's destination from its claimed tags: a work created this
// instant has no WorkDetail yet, so enrichment's canonical names show up later as a rename diff.
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

// keptFileName is "rename on import" turned off: the template still decides
// which folders a file lands in, and the file itself keeps the name it
// arrived with, sanitised for the destination filesystem the way every
// rendered component already is.
func keptFileName(renderedRelPath, sourceRelPath string) string {
	kept := fsx.SanitiseComponent(strings.TrimSpace(path.Base(sourceRelPath)))
	if kept == "" {
		return renderedRelPath
	}
	return path.Join(path.Dir(renderedRelPath), kept)
}
