package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/fsx"
	"github.com/makearr/MediaNexus/internal/library"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/scan"
	"github.com/makearr/MediaNexus/internal/settings"
)

// DefaultAcceptThreshold and DefaultAmbiguityThreshold tune the ADR-24 chain.
// MBIDIdentifier's confidences (0.95/0.85/0.70, identifier.go) all clear
// AcceptThreshold, so a tagged file never pays for a search; SearchIdentifier's
// ceiling (0.65, search_identifier.go) stays below it, so a search match never
// stops the chain early either -- it can only be the final answer, not a shortcut
// past a strategy that might still find a real MBID.
const (
	DefaultAcceptThreshold    = 0.9
	DefaultAmbiguityThreshold = 0.6
)

// Categories answers the one policy question discovery needs: what a scan
// should do with an album nobody asked for (section 19.6). Asked fresh per
// batch rather than cached, the same "ask, don't cache" discipline scan.Roots
// already uses for root folders.
type Categories interface {
	Category(id int64) (settings.Category, bool)
}

// CategoryFinder resolves which category a media type's own discovery should
// use when nothing more specific is configured -- subscription.expand's own
// need (a subscription has no root folder to inherit a category from the way
// a scan does), distinct from Categories' by-id lookup.
type CategoryFinder interface {
	CategoryForMediaType(mediaType string) (settings.Category, bool)
}

// Consumer records what a scan found: media_object and library_path rows always,
// the work it identifies when the MBID chain resolves one, and -- when it does
// not -- an F29 manual-identification-queue entry with whatever candidates the
// chain did find. When Desired and Categories are configured it also does what
// section 19.6 calls "a scan creates desired items too": an album-level SET item
// per work it identifies, with per-track alignment (Align) attaching whichever
// of this batch's files match a still-uncovered part. It implements
// scan.Consumer, direct-wired the way the rest of internal/music is until
// stage 9.
type Consumer struct {
	fsys         fsx.FS
	physical     library.Physical
	identity     library.Identity
	unidentified library.Unidentified
	search       Searcher
	clock        clock.Clock
	chain        model.ChainConfig
	logger       *slog.Logger

	// desired, categories and defaultProfileID are all optional together: a
	// caller (or test) that leaves them unset gets identification with no
	// discovery, never a panic -- discoverWanted checks desired/categories
	// before touching either.
	desired          *library.Desired
	categories       Categories
	defaultProfileID int64
}

// ConsumerConfig is what a Consumer needs. Every field is required except
// Chain, Search, Logger, and the Desired/Categories/DefaultProfileID trio
// (section 19.6's discovery -- omit all three together to get identification
// with no discovery).
type ConsumerConfig struct {
	FS           fsx.FS
	Physical     library.Physical
	Identity     library.Identity
	Unidentified library.Unidentified
	// Search adds the ADR-24 "parse" strategy (SearchIdentifier) to the chain,
	// for a file MBIDIdentifier cannot answer at all. Nil leaves the chain at the
	// one strategy stage 3 shipped with -- a media type or test with no metadata
	// provider configured is unaffected.
	Search Searcher
	Clock  clock.Clock
	// Chain tunes RunChain (model.ChainConfig). The zero value takes the package
	// defaults above.
	Chain  model.ChainConfig
	Logger *slog.Logger
	// Desired, Categories and DefaultProfileID together turn on section 19.6's
	// scan-time discovery. DefaultProfileID is used when a category has no
	// quality profile of its own configured yet (todo 4.3's own gap).
	Desired          *library.Desired
	Categories       Categories
	DefaultProfileID int64
}

// NewConsumer builds it.
func NewConsumer(config ConsumerConfig) Consumer {
	chain := config.Chain
	if chain == (model.ChainConfig{}) {
		chain = model.ChainConfig{
			AcceptThreshold: DefaultAcceptThreshold, AmbiguityThreshold: DefaultAmbiguityThreshold,
		}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return Consumer{
		fsys: config.FS, physical: config.Physical, identity: config.Identity,
		unidentified: config.Unidentified, search: config.Search,
		clock: config.Clock, chain: chain, logger: logger,
		desired: config.Desired, categories: config.Categories, defaultProfileID: config.DefaultProfileID,
	}
}

// Observe implements scan.Consumer. Every file's bytes are read once, before any
// transaction opens (Invariant 2): quick hashes first, one registration transaction,
// then the chain (pure Go, no I/O) decides identity, then one more transaction records
// whatever it resolved. A failure at any point leaves nothing lost -- the next sweep
// resumes from the un-advanced cursor and finds the same, still-idempotent work to do.
func (c Consumer) Observe(
	ctx context.Context, root settings.RootFolder, categoryID int64, batch []scan.Observed,
) error {
	readable := make([]scan.Observed, 0, len(batch))
	for _, observed := range batch {
		if observed.Probe.Err == nil {
			readable = append(readable, observed)
		}
	}
	if len(readable) == 0 {
		return nil
	}

	now := c.clock.Now()
	files := make([]library.PhysicalFile, len(readable))
	for index, observed := range readable {
		absolute := path.Join(root.Path, observed.File.RelPath)
		hash, err := library.QuickHash(c.fsys, absolute, observed.File.SizeBytes)
		if err != nil {
			return fmt.Errorf("music: %w", err)
		}
		files[index] = library.PhysicalFile{
			RelPath: observed.File.RelPath, SizeBytes: observed.File.SizeBytes,
			QuickHash: hash, FirstSeenAt: now,
		}
	}

	mediaObjectIDs, err := c.physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}

	tags := tagSourceFor(root, readable)
	identifiers := []model.Identifier{NewMBIDIdentifier(tags)}
	if c.search != nil {
		identifiers = append(identifiers, NewSearchIdentifier(SearchIdentifierConfig{Tags: tags, Search: c.search}))
	}
	links, trackFiles, queued, resolved := c.identifyBatch(ctx, root.ID, readable, mediaObjectIDs, identifiers)
	workIDs, err := c.identity.ResolveWorks(ctx, now, links)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}
	if err := c.unidentified.Record(ctx, queued); err != nil {
		return fmt.Errorf("music: %w", err)
	}
	// A file that resolves this sweep may have been queued by an earlier one --
	// clearing is a no-op otherwise, so this runs unconditionally rather than
	// tracking which ids were ever queued.
	if err := c.unidentified.Clear(ctx, resolved); err != nil {
		return fmt.Errorf("music: %w", err)
	}
	if err := c.discoverWanted(ctx, categoryID, workIDs, trackFiles); err != nil {
		return fmt.Errorf("music: %w", err)
	}

	c.logger.Info("scan batch recorded",
		slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("identified", len(links)), slog.Int("queued", len(queued)))
	return nil
}

// identifyBatch runs the ADR-24 chain per file. links is what resolved and should
// be written to the library; trackFiles is that same file's own claimed
// disc/track/title/recording-id (section 19.6), positionally lined up with
// links for discoverWanted to use once ResolveWorks says which work each one
// belongs to; queued is every file whose merged result did not clear
// AmbiguityThreshold, bound for the F29 manual identification queue with
// whatever candidates RunChain still found; resolved is the media_object ids
// that did identify, so a previously queued entry for one of them can be
// cleared.
func (c Consumer) identifyBatch(
	ctx context.Context, rootFolderID int64, observed []scan.Observed, mediaObjectIDs []int64,
	identifiers []model.Identifier,
) (links []library.WorkLink, trackFiles []TrackFile, queued []library.QueueEntry, resolved []int64) {
	now := c.clock.Now().Unix()
	for index, one := range observed {
		mediaObjectID := mediaObjectIDs[index]
		fileRef := model.FileRef{
			MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: one.File.RelPath,
		}
		result, err := model.RunChain(ctx, identifiers, fileRef, c.chain)
		if err != nil {
			// A strategy failing to run is not a reason to fail the whole batch --
			// this file simply stays unidentified for the next sweep to retry, and
			// is not queued: a transient failure is not evidence there is nothing
			// to find, so it should not display "no candidates" until it is.
			c.logger.Warn("identify failed", slog.String("path", one.File.RelPath), slog.Any("error", err))
			continue
		}
		// Best.ExternalID is unset only if a future strategy resolves straight to a
		// local WorkID (ADR-24); none does yet, so there is nothing to link there --
		// treated the same as an unresolved chain below.
		if result.Best == nil || result.Best.ExternalID == nil {
			queued = append(queued, library.QueueEntry{
				MediaObjectID: mediaObjectID, RootFolderID: rootFolderID, RelPath: one.File.RelPath,
				Candidates: result.Candidates, At: now,
			})
			continue
		}
		resolved = append(resolved, mediaObjectID)
		links = append(links, library.WorkLink{
			Provider: result.Best.ExternalID.Provider, ExternalID: result.Best.ExternalID.ExternalID,
			Confidence: result.Best.ExternalID.Confidence, Work: newWorkIdentity(one),
			Edition: newEditionLink(one), RootFolderID: rootFolderID, MediaObjectID: mediaObjectID,
		})
		trackFiles = append(trackFiles, ReadTrackFile(mediaObjectID, one.Probe.Tags))
	}
	return links, trackFiles, queued, resolved
}

// discoverWanted is section 19.6's "a scan creates desired items too": for each
// work this batch resolved a file to, ensure its album-level SET item exists
// (per category.monitor_discovered policy) and align this batch's own files
// against whatever parts are not yet covered (Align). A work whose tracklist
// has not been enriched yet still gets its item -- required_count stays
// unknown until EnrichJob adds parts, and a later full sweep re-attaches this
// batch's files once it does (workdetail.go's OnDisk doc comment records the
// same ordering dependency one level up).
//
// Skipped entirely when Desired/Categories were not configured, or when
// categoryID is 0 (a caller with no category context, such as a test).
func (c Consumer) discoverWanted(ctx context.Context, categoryID int64, workIDs []int64, trackFiles []TrackFile) error {
	if c.desired == nil || c.categories == nil || categoryID == 0 {
		return nil
	}
	category, found := c.categories.Category(categoryID)
	if !found || category.MonitorDiscovered == settings.MonitorDiscoveredNone {
		return nil
	}

	byWork := make(map[int64][]TrackFile)
	for index, workID := range workIDs {
		byWork[workID] = append(byWork[workID], trackFiles[index])
	}

	profileID := c.defaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	monitored := category.MonitorDiscovered == settings.MonitorDiscoveredMonitored
	now := c.clock.Now().Unix()

	for workID, candidates := range byWork {
		item, err := c.desired.EnsureAlbumItem(ctx, library.AlbumRequest{
			WorkID: workID, CategoryID: categoryID, ProfileID: profileID, Monitored: monitored,
		})
		if err != nil {
			return fmt.Errorf("ensure album item for work %d: %w", workID, err)
		}

		unfilled, err := c.desired.UnfilledParts(ctx, item.ID, workID)
		if err != nil {
			return fmt.Errorf("unfilled parts for work %d: %w", workID, err)
		}
		if len(unfilled) == 0 {
			continue
		}
		recordingMBIDs, err := c.desired.ExternalIDsFor(ctx, unfilled, ProviderRecording)
		if err != nil {
			return fmt.Errorf("recording mbids for work %d: %w", workID, err)
		}
		parts := make([]Part, len(unfilled))
		for index, part := range unfilled {
			parts[index] = Part{
				PartID: part.ID, MediaRef: part.MediaRef, Title: part.Title.String,
				RecordingMBID: recordingMBIDs[part.ID],
			}
		}

		var aligned []library.AlignedFile
		for _, alignedPart := range Align(parts, candidates).Parts {
			if alignedPart.File != nil {
				aligned = append(aligned, library.AlignedFile{
					MediaObjectID: alignedPart.File.MediaObjectID, MediaRef: alignedPart.Part.MediaRef,
				})
			}
		}
		if len(aligned) == 0 {
			continue
		}
		if err := c.desired.RecordAlignment(ctx, item.ID, workID, aligned, now); err != nil {
			return fmt.Errorf("record alignment for work %d: %w", workID, err)
		}
	}
	return nil
}

// tagSourceFor answers the chain's tag lookups from what the scan already probed --
// ADR-28/ADR-30's "one ffprobe invocation per file, not per identifier".
func tagSourceFor(root settings.RootFolder, observed []scan.Observed) Resolved {
	probes := make([]mediatool.Probe, len(observed))
	for index, one := range observed {
		probes[index] = one.Probe
	}
	return Resolved{
		Tags: NewBatchTags(probes),
		Path: func(file model.FileRef) (string, bool) { return path.Join(root.Path, file.RelPath), true },
	}
}

// newWorkIdentity is what to name a work if the chain's identity needs one created.
// The album tag names it; a file tagged sparsely falls back to its containing
// directory, which is how a Lidarr-managed library already names an album folder.
func newWorkIdentity(observed scan.Observed) library.WorkIdentity {
	title := observed.Probe.Tags.First("album")
	if title == "" {
		title = albumTitleFallback(observed.File.RelPath)
	}
	return library.WorkIdentity{MediaType: "music", CanonicalTitle: title, SortTitle: sortTitle(title)}
}

// newEditionLink resolves the release (edition-level) MBID, when the file carries
// one, into a placeholder edition -- kind "unknown", label the release MBID itself.
// A guessed human label risks colliding with a real edition's UNIQUE(work_id, kind,
// label) once the MusicBrainz provider exists; an MBID as its own label cannot,
// since no real label is ever a UUID, and the provider updates this row in place
// rather than needing to create a second one.
func newEditionLink(observed scan.Observed) *library.EditionLink {
	release := ReadMBIDs(observed.Probe.Tags).Release
	if release == "" {
		return nil
	}
	return &library.EditionLink{
		Provider: ProviderRelease, ExternalID: release, Confidence: 1.0,
		Edition: library.EditionIdentity{Kind: "unknown", Label: release},
	}
}
