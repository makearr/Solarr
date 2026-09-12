package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/moving/mover"
	"github.com/makearr/MediaNexus/internal/moving/naming"
	"github.com/makearr/MediaNexus/internal/moving/placement"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
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
}

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
}

// NewCommitter builds one.
func NewCommitter(config CommitterConfig) Committer {
	return Committer{
		fsys: config.FS, roots: config.Roots, affinity: config.Affinity, physical: config.Physical,
		identity: config.Identity, desired: config.Desired, categories: config.Categories,
		blockedContent: config.BlockedContent, clock: config.Clock,
		defaultProfileID: config.DefaultProfileID,
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

// CommitUnit places unit (found under sourceRoot by inspect.Run) into
// categoryID's own library. best is the unit's own DECIDE-cleared
// Identification (model.ChainResult.Best); tags is the same Resolved batch
// IDENTIFY already probed, reused here rather than a second probe
// (ADR-48's own speed rule: "probe only what identification will use").
func (c Committer) CommitUnit(
	ctx context.Context, sourceRoot string, unit inspect.Unit, best model.Identification, tags Resolved, categoryID int64,
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
	artist := representative.First("albumartist", "album_artist", "artist")
	year := parseYear(representative.First("date", "year"))

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

	files := make([]library.PhysicalFile, len(unit.Media))
	trackFiles := make([]TrackFile, len(unit.Media))
	for index, media := range unit.Media {
		fileTags, err := tags.TagsFor(ctx, model.FileRef{RelPath: media.RelPath})
		if err != nil {
			return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
		}
		track := ReadTrackFile(0, fileTags)
		destRelPath, err := importRelPath(work, artist, year, track, path.Ext(media.RelPath))
		if err != nil {
			return Placed{}, fmt.Errorf("music: commit unit %q: %w", unit.Directory, err)
		}

		sourceAbs := path.Join(sourceRoot, media.RelPath)
		destAbs := path.Join(destRoot.Path, destRelPath)
		if _, err := mover.PlaceOne(c.fsys, sourceAbs, destAbs, media.SizeBytes); err != nil {
			return Placed{}, fmt.Errorf("music: place %q: %w", media.RelPath, err)
		}
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
	for index, file := range registered {
		mediaObjectIDs[index] = file.MediaObjectID
		libraryPathIDs[index] = file.LibraryPathID
		trackFiles[index].MediaObjectID = file.MediaObjectID
	}

	link := library.WorkLink{
		Provider: best.ExternalID.Provider, ExternalID: best.ExternalID.ExternalID,
		Confidence: best.ExternalID.Confidence, Work: work, Edition: edition,
		RootFolderID: destRoot.ID, MediaObjectID: mediaObjectIDs[0],
	}
	resolutions, err := c.identity.ResolveWorksDetailed(ctx, now, []library.WorkLink{link})
	if err != nil {
		return Placed{}, fmt.Errorf("music: resolve identity for unit %q: %w", unit.Directory, err)
	}
	resolution := resolutions[0]

	if err := DiscoverWanted(
		ctx, c.desired, c.categories, c.clock, c.defaultProfileID, categoryID, resolution.WorkID, trackFiles,
	); err != nil {
		return Placed{}, fmt.Errorf("music: discover wanted for unit %q: %w", unit.Directory, err)
	}

	return Placed{
		WorkID: resolution.WorkID, WorkCreated: resolution.WorkCreated,
		EditionID: resolution.EditionID, EditionCreated: resolution.EditionCreated,
		MediaObjectIDs: mediaObjectIDs, LibraryPathIDs: libraryPathIDs,
	}, nil
}

// rootCandidates answers every root folder attached to categoryID, in
// placement.RootCandidate form -- the same conversion
// internal/moving/drainjob.rootCandidate already does for drain, duplicated
// rather than shared since drainjob's own is unexported and the two
// packages must not import each other (both sit beside, not above, the
// other in architecture §2's graph).
func (c Committer) rootCandidates(categoryID int64) []placement.RootCandidate {
	acceptsNew := map[int64]bool{}
	var order []int64
	seen := map[int64]bool{}
	for _, attachment := range c.roots.CategoryRoots() {
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
		root, ok := c.roots.RootFolder(id)
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

// importRelPath renders one file's destination path under the shipped
// default template, from claimed tags directly rather than a DB WorkDetail
// (music.NamingContext's own input) -- a work created this instant has no
// WorkDetail worth reading yet (its parts, and any real artist credit, are
// EnrichJob's job, not this one's), the same accepted gap a scan-discovered
// work's own rename preview already carries (notes.md §5: "{Artist Name}
// renders empty"). Once enrichment lands a canonical artist name that
// differs from the tag's raw string, this file's own rename preview will
// show a diff -- expected, not a bug, the same way any scan-discovered
// work's does today. No edition/Format token: a freshly resolved edition is
// the "unknown" placeholder (unitEditionLink), which carries no format.
func importRelPath(work library.WorkIdentity, artist string, year *int64, track TrackFile, extension string) (string, error) {
	ctx := naming.Context{TokenAlbumTitle: work.CanonicalTitle}
	if artist != "" {
		ctx[TokenArtistName] = artist
	}
	if year != nil {
		ctx[TokenReleaseYear] = strconv.FormatInt(*year, 10)
	}
	if track.Title != "" {
		ctx[TokenTrackTitle] = track.Title
	}
	ctx[TokenDisc] = strconv.Itoa(track.Disc)
	ctx[TokenTrack] = strconv.Itoa(track.Track)

	rendered, _, err := naming.RenderPath(compiledDefaultFolder, compiledDefaultFile, ctx, naming.DefaultSeparator, extension)
	if err != nil {
		return "", fmt.Errorf("render path: %w", err)
	}
	return rendered, nil
}
