package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strings"

	"github.com/makearr/MediaNexus/internal/db/library"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/fsx"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
	"github.com/makearr/MediaNexus/internal/scanning/scan"
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
// scan.Consumer, direct-wired the way the rest of internal/media/music is until
// stage 9.
type Consumer struct {
	fsys         fsx.FS
	physical     library.Physical
	identity     library.Identity
	unidentified library.Unidentified
	search       Searcher
	albums       AlbumLookup
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
	// Albums identifies a folder's untagged files together against whole tracklists.
	// Nil leaves identification per file.
	Albums AlbumLookup
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
		unidentified: config.Unidentified, search: config.Search, albums: config.Albums,
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

	registered, err := c.physical.RegisterBatch(ctx, root.ID, files)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}
	mediaObjectIDs := make([]int64, len(registered))
	for index, file := range registered {
		mediaObjectIDs[index] = file.MediaObjectID
	}
	if err := c.recordClaimedAttributes(ctx, readable, mediaObjectIDs); err != nil {
		return fmt.Errorf("music: %w", err)
	}

	tags := tagSourceFor(root, readable)
	embedded := []model.Identifier{NewMBIDIdentifier(tags)}
	var search []model.Identifier
	if c.search != nil {
		search = []model.Identifier{NewSearchIdentifier(SearchIdentifierConfig{Tags: tags, Search: c.search})}
	}
	links, trackFiles, queued, resolved := c.identifyBatch(ctx, root.ID, readable, mediaObjectIDs, embedded, search)
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

// identifyBatch identifies a directory batch in passes: embedded MBIDs, whole-album matching,
// per-file text search, folder siblings. queued feeds the F29 queue; resolved clears it.
func (c Consumer) identifyBatch(
	ctx context.Context, rootFolderID int64, observed []scan.Observed, mediaObjectIDs []int64,
	embedded, search []model.Identifier,
) (links []library.WorkLink, trackFiles []TrackFile, queued []library.QueueEntry, resolved []int64) {
	out := batchResult{rootFolderID: rootFolderID, observed: observed, mediaObjectIDs: mediaObjectIDs, at: c.clock.Now().Unix()}
	all := make([]pendingFile, len(observed))
	for index := range observed {
		all[index] = pendingFile{index: index}
	}

	pending := c.runChain(ctx, &out, embedded, all)
	if c.albums != nil && len(pending) > 0 {
		pending = c.identifyAlbums(ctx, &out, pending)
	}
	if len(search) > 0 && len(pending) > 0 {
		pending = c.runChain(ctx, &out, search, pending)
	}

	// A file nothing could identify joins its folder's album when every
	// identified file in the folder agreed on one (approved 2026-09-17: 127 of
	// 140 unidentified test files sat beside siblings like that).
	sibling, agreed := soleSiblingIdentity(out.links)
	for _, file := range pending {
		if !agreed {
			out.queue(file.index, file.candidates)
			continue
		}
		one := observed[file.index]
		out.identify(file.index, library.WorkLink{
			Provider: sibling.Provider, ExternalID: sibling.ExternalID, Confidence: siblingConfidence,
			Work: newWorkIdentity(one), RootFolderID: rootFolderID, MediaObjectID: mediaObjectIDs[file.index],
		}, ReadTrackFile(mediaObjectIDs[file.index], one.Probe.Tags))
	}
	return out.links, out.trackFiles, out.queued, out.resolved
}

// batchResult accumulates what identifyBatch's passes decide for one batch.
type batchResult struct {
	rootFolderID   int64
	observed       []scan.Observed
	mediaObjectIDs []int64
	at             int64

	links      []library.WorkLink
	trackFiles []TrackFile
	queued     []library.QueueEntry
	resolved   []int64
}

func (r *batchResult) identify(index int, link library.WorkLink, file TrackFile) {
	r.resolved = append(r.resolved, r.mediaObjectIDs[index])
	r.links = append(r.links, link)
	r.trackFiles = append(r.trackFiles, file)
}

func (r *batchResult) queue(index int, candidates []model.Identification) {
	r.queued = append(r.queued, library.QueueEntry{
		MediaObjectID: r.mediaObjectIDs[index], RootFolderID: r.rootFolderID,
		RelPath: r.observed[index].File.RelPath, Candidates: candidates, At: r.at,
	})
}

// pendingFile is a batch file no pass has identified yet, with the best candidates seen so far.
type pendingFile struct {
	index      int
	candidates []model.Identification
}

// runChain runs the ADR-24 chain per pending file, answering what is still unresolved; a file
// whose strategy failed to run is dropped unqueued, so a transient failure never reads as "no candidates".
func (c Consumer) runChain(ctx context.Context, out *batchResult, identifiers []model.Identifier, files []pendingFile) []pendingFile {
	var pending []pendingFile
	for _, file := range files {
		one := out.observed[file.index]
		mediaObjectID := out.mediaObjectIDs[file.index]
		fileRef := model.FileRef{MediaObjectID: &mediaObjectID, RootFolderID: &out.rootFolderID, RelPath: one.File.RelPath}
		result, err := model.RunChain(ctx, identifiers, fileRef, c.chain)
		if err != nil {
			c.logger.Warn("identify failed", slog.String("path", one.File.RelPath), slog.Any("error", err))
			continue
		}
		if result.Best == nil || result.Best.ExternalID == nil {
			if len(result.Candidates) > 0 {
				file.candidates = result.Candidates
			}
			pending = append(pending, file)
			continue
		}
		out.identify(file.index, library.WorkLink{
			Provider: result.Best.ExternalID.Provider, ExternalID: result.Best.ExternalID.ExternalID,
			Confidence: result.Best.ExternalID.Confidence, Work: newWorkIdentity(one),
			Edition: newEditionLink(one), RootFolderID: out.rootFolderID, MediaObjectID: mediaObjectID,
		}, ReadTrackFile(mediaObjectID, one.Probe.Tags))
	}
	return pending
}

// identifyAlbums matches pending files album by album: identified links each file to the release,
// ambiguous is queued rather than guessed per file, anything else stays pending for later passes.
func (c Consumer) identifyAlbums(ctx context.Context, out *batchResult, files []pendingFile) []pendingFile {
	var seeds []string
	if sibling, agreed := soleSiblingIdentity(out.links); agreed && sibling.Provider == ProviderReleaseGroup {
		seeds = []string{sibling.ExternalID}
	}

	var pending []pendingFile
	for _, group := range groupByAlbum(out.observed, files) {
		locals := make([]LocalTrack, len(group))
		for i, file := range group {
			one := out.observed[file.index]
			locals[i] = ReadLocalTrack(one.Probe.Tags, one.Probe.Measured, one.File.RelPath)
		}
		decision, err := IdentifyAlbum(ctx, c.albums, musicbrainz.PriorityIdentify, locals, seeds)
		if err != nil {
			c.logger.Warn("album identification failed",
				slog.String("path", out.observed[group[0].index].File.RelPath), slog.Any("error", err))
			pending = append(pending, group...)
			continue
		}
		switch decision.Outcome {
		case AlbumIdentified:
			for i, file := range group {
				out.identify(file.index, albumLink(out, file.index, *decision.Best, decision.Best.Tracks[i]),
					albumTrackFile(out, file.index, decision.Best.Tracks[i]))
			}
		case AlbumAmbiguous:
			candidates := AlbumCandidates(decision)
			for _, file := range group {
				out.queue(file.index, candidates)
			}
		default:
			pending = append(pending, group...)
		}
	}
	return pending
}

// groupByAlbum splits pending files by their album tag; untagged files join the only tagged
// album when there is one, and form their own group otherwise.
func groupByAlbum(observed []scan.Observed, files []pendingFile) [][]pendingFile {
	var keys []string
	groups := make(map[string][]pendingFile)
	for _, file := range files {
		tags := observed[file.index].Probe.Tags
		key := ""
		if album := strings.ToLower(strings.TrimSpace(tags.First("album"))); album != "" {
			key = strings.ToLower(strings.TrimSpace(tags.First("albumartist", "album_artist", "artist"))) + "\x1f" + album
		}
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], file)
	}
	if untagged, found := groups[""]; found && len(keys) == 2 {
		tagged := keys[0]
		if tagged == "" {
			tagged = keys[1]
		}
		groups[tagged] = append(groups[tagged], untagged...)
		delete(groups, "")
		keys = []string{tagged}
	}
	result := make([][]pendingFile, 0, len(keys))
	for _, key := range keys {
		if group, found := groups[key]; found {
			result = append(result, group)
		}
	}
	return result
}

// albumLink links one file of an identified album to its release group and release; a file
// the release has no track for sits at sibling confidence, since only its folder says so.
func albumLink(out *batchResult, index int, best ReleaseScore, track *ReleaseTrack) library.WorkLink {
	confidence := albumConfidence(best.Distance)
	if track == nil {
		confidence = siblingConfidence
	}
	release := best.Candidate.Release.MBID
	return library.WorkLink{
		Provider: ProviderReleaseGroup, ExternalID: best.Candidate.ReleaseGroup.MBID, Confidence: confidence,
		Work: newWorkIdentity(out.observed[index]), RootFolderID: out.rootFolderID, MediaObjectID: out.mediaObjectIDs[index],
		Edition: &library.EditionLink{
			Provider: ProviderRelease, ExternalID: release, Confidence: confidence,
			Edition: library.EditionIdentity{Kind: "unknown", Label: release},
		},
	}
}

// albumTrackFile is the file as its matched track, so wanting and alignment use the
// release's own position and recording rather than the file's possibly missing tags.
func albumTrackFile(out *batchResult, index int, track *ReleaseTrack) TrackFile {
	file := ReadTrackFile(out.mediaObjectIDs[index], out.observed[index].Probe.Tags)
	if track != nil {
		file.Disc, file.Track, file.RecordingMBID = track.Disc, track.Position, track.RecordingMBID
		if file.Title == "" {
			file.Title = track.Title
		}
	}
	return file
}

// siblingConfidence is below anything a strategy reports for the file itself:
// the evidence is where the file sits, not what it says.
const siblingConfidence = 0.5

// soleSiblingIdentity is the one album every identified file in a folder
// resolved to, or false when there were none or they disagreed.
func soleSiblingIdentity(links []library.WorkLink) (library.WorkLink, bool) {
	if len(links) == 0 {
		return library.WorkLink{}, false
	}
	for _, link := range links[1:] {
		if link.Provider != links[0].Provider || link.ExternalID != links[0].ExternalID {
			return library.WorkLink{}, false
		}
	}
	return links[0], true
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
	byWork := make(map[int64][]TrackFile)
	for index, workID := range workIDs {
		byWork[workID] = append(byWork[workID], trackFiles[index])
	}
	for workID, candidates := range byWork {
		if err := DiscoverWanted(
			ctx, c.desired, c.categories, c.clock, c.defaultProfileID, categoryID, workID, candidates,
		); err != nil {
			return err
		}
	}
	return nil
}

// DiscoverWanted is section 19.6's own "a scan creates desired items too",
// factored out of Consumer.discoverWanted so import commit (Committer) runs
// the identical policy for a freshly placed file rather than a second,
// driftable copy of it: ensure workID's album-level SET item exists (per
// category.monitor_discovered policy) and align candidates -- this caller's
// own batch of files, already known to belong to workID -- against whatever
// parts are not yet covered (Align). A work whose tracklist has not been
// enriched yet still gets its item -- required_count stays unknown until
// EnrichJob adds parts, and a later full sweep re-attaches this batch's
// files once it does.
//
// A no-op when desired/categories is nil, categoryID is 0, or the category
// is not found or configured monitor_discovered=none.
func DiscoverWanted(
	ctx context.Context, desired *library.Desired, categories Categories, cl clock.Clock,
	defaultProfileID, categoryID, workID int64, candidates []TrackFile,
) error {
	if desired == nil || categories == nil || categoryID == 0 {
		return nil
	}
	category, found := categories.Category(categoryID)
	if !found || category.MonitorDiscovered == settings.MonitorDiscoveredNone {
		return nil
	}
	profileID := defaultProfileID
	if category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	monitored := category.MonitorDiscovered == settings.MonitorDiscoveredMonitored
	now := cl.Now().Unix()

	item, err := desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: workID, CategoryID: categoryID, ProfileID: profileID, Monitored: monitored,
	})
	if err != nil {
		return fmt.Errorf("ensure album item for work %d: %w", workID, err)
	}

	unfilled, err := desired.UnfilledParts(ctx, item.ID, workID)
	if err != nil {
		return fmt.Errorf("unfilled parts for work %d: %w", workID, err)
	}
	if len(unfilled) == 0 {
		return nil
	}
	recordingMBIDs, err := desired.ExternalIDsFor(ctx, unfilled, ProviderRecording)
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
		return nil
	}
	return desired.RecordAlignment(ctx, item.ID, workID, aligned, now)
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
	return library.WorkIdentity{MediaType: "music", CanonicalTitle: title, SortTitle: SortTitle(title)}
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
