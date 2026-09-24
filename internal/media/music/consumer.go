package music

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// DefaultAcceptThreshold stops the chain once a candidate is that likely right; a release-group
// or release tag clears it, so a tagged file never pays for a search. DefaultAmbiguityThreshold
// is the one identification threshold (import.auto_commit_confidence's default), for scan and
// import alike since confidence became a calibrated likelihood (2026-09-24).
const (
	DefaultAcceptThreshold    = 0.9
	DefaultAmbiguityThreshold = 0.9
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
// a scan does). It includes the by-id lookup, since a subscription that
// recorded its own library uses that in preference (2026-09-19).
type CategoryFinder interface {
	Categories
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
	attempts     *library.Attempts
	search       Searcher
	albums       AlbumLookup
	fingerprints Fingerprinter
	acoustID     AcoustIDLookup
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
	threshold        func() float64
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
	// Attempts records each file's identification run beyond its embedded MBIDs; nil records none.
	Attempts *library.Attempts
	// Search adds the ADR-24 "parse" strategy (SearchIdentifier) to the chain,
	// for a file MBIDIdentifier cannot answer at all. Nil leaves the chain at the
	// one strategy stage 3 shipped with -- a media type or test with no metadata
	// provider configured is unaffected.
	Search Searcher
	// Albums identifies a folder's untagged files together against whole tracklists.
	// Nil leaves identification per file.
	Albums AlbumLookup
	// Fingerprints and AcoustID together add ADR-29's fingerprint rung, for a
	// file every earlier pass left unnamed. Either one nil leaves it out.
	Fingerprints Fingerprinter
	AcoustID     AcoustIDLookup
	Clock        clock.Clock
	// Chain tunes RunChain (model.ChainConfig). The zero value takes the package
	// defaults above.
	Chain model.ChainConfig
	// Threshold is the configured identification threshold, read per batch so a change applies
	// without a restart; nil keeps Chain's AmbiguityThreshold.
	Threshold func() float64
	Logger    *slog.Logger
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
		unidentified: config.Unidentified, attempts: config.Attempts, search: config.Search, albums: config.Albums,
		fingerprints: config.Fingerprints, acoustID: config.AcoustID,
		clock: config.Clock, chain: chain, threshold: config.Threshold, logger: logger,
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
	c.changeFileDates(root, categoryID, readable)

	// A person's identification always wins: those files are registered, never identified again.
	readable, mediaObjectIDs, err = c.withoutManual(ctx, readable, mediaObjectIDs)
	if err != nil {
		return fmt.Errorf("music: %w", err)
	}
	if len(readable) == 0 {
		return nil
	}

	tags := tagSourceFor(root, readable)
	passes := passIdentifiers{embedded: []model.Identifier{NewMBIDIdentifier(tags)}}
	if c.search != nil {
		passes.search = []model.Identifier{NewSearchIdentifier(SearchIdentifierConfig{Tags: tags, Search: c.search})}
	}
	if c.fingerprints != nil && c.acoustID != nil {
		passes.fingerprint = []model.Identifier{NewAcoustIDIdentifier(AcoustIDIdentifierConfig{
			Fingerprints: c.fingerprints, Lookup: c.acoustID,
			Absolute: func(relPath string) string { return path.Join(root.Path, relPath) },
		})}
	}
	out := c.identifyBatch(ctx, root.ID, root.Path, readable, mediaObjectIDs, passes)
	links, trackFiles, queued, resolved := out.links, out.trackFiles, out.queued, out.resolved
	if c.attempts != nil {
		if err := c.attempts.Record(ctx, out.attempts()); err != nil {
			return fmt.Errorf("music: %w", err)
		}
	}
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

// passIdentifiers are the chain's identifiers per pass; an empty slice skips its pass.
type passIdentifiers struct {
	embedded    []model.Identifier
	search      []model.Identifier
	fingerprint []model.Identifier
}

// identifyBatch identifies a directory batch in passes: embedded MBIDs, whole-album matching,
// per-file text search, fingerprints, folder siblings. queued feeds the F29 queue; resolved clears it.
func (c Consumer) identifyBatch(
	ctx context.Context, rootFolderID int64, rootPath string, observed []scan.Observed, mediaObjectIDs []int64,
	passes passIdentifiers,
) *batchResult {
	out := &batchResult{
		rootFolderID: rootFolderID, rootPath: rootPath, observed: observed, mediaObjectIDs: mediaObjectIDs,
		at:     c.clock.Now().Unix(),
		passes: make(map[int][]library.AttemptPass), outcomes: make(map[int]string),
	}
	all := make([]pendingFile, len(observed))
	for index := range observed {
		all[index] = pendingFile{index: index}
	}

	pending := c.runChain(ctx, out, PassEmbeddedTags, passes.embedded, all)
	if c.albums != nil && len(pending) > 0 {
		pending = c.identifyAlbums(ctx, out, pending)
	}
	if len(passes.search) > 0 && len(pending) > 0 {
		pending = c.runChain(ctx, out, PassTextSearch, passes.search, pending)
	}
	if len(passes.fingerprint) > 0 && len(pending) > 0 {
		pending = c.fingerprintPass(ctx, out, passes.fingerprint, pending)
	}

	// A file nothing could identify joins its folder's album when every
	// identified file in the folder agreed on one (approved 2026-09-17: 127 of
	// 140 unidentified test files sat beside siblings like that).
	sibling, agreed := soleSiblingIdentity(out.links)
	for _, file := range pending {
		if !agreed {
			outcome := PassOutcomeNoIdentifiedSibling
			if len(out.links) > 0 {
				outcome = PassOutcomeSiblingsDisagree
			}
			out.note(file.index, library.AttemptPass{Pass: PassFolderSiblings, Outcome: outcome})
			out.queue(file.index, file.candidates)
			continue
		}
		one := observed[file.index]
		out.note(file.index, library.AttemptPass{Pass: PassFolderSiblings, Outcome: PassOutcomeJoined})
		out.identify(file.index, library.WorkLink{
			Provider: sibling.Provider, ExternalID: sibling.ExternalID, Confidence: siblingConfidence,
			Work: newWorkIdentity(one), RootFolderID: rootFolderID, MediaObjectID: mediaObjectIDs[file.index],
		}, ReadTrackFile(mediaObjectIDs[file.index], one.Probe.Tags))
	}
	return out
}

// batchResult accumulates what identifyBatch's passes decide for one batch.
type batchResult struct {
	rootFolderID   int64
	rootPath       string
	observed       []scan.Observed
	mediaObjectIDs []int64
	at             int64

	links      []library.WorkLink
	trackFiles []TrackFile
	queued     []library.QueueEntry
	resolved   []int64

	// passes and outcomes are the attempt record, per batch index, for files past their embedded tags.
	passes   map[int][]library.AttemptPass
	outcomes map[int]string
}

// noted reports whether this file already has a record for that pass.
func (r *batchResult) noted(index int, pass string) bool {
	return slices.ContainsFunc(r.passes[index], func(p library.AttemptPass) bool { return p.Pass == pass })
}

func (r *batchResult) identify(index int, link library.WorkLink, file TrackFile) {
	r.resolved = append(r.resolved, r.mediaObjectIDs[index])
	r.links = append(r.links, link)
	r.trackFiles = append(r.trackFiles, file)
	r.outcomes[index] = AttemptIdentified
}

func (r *batchResult) queue(index int, candidates []model.Identification) {
	r.queued = append(r.queued, library.QueueEntry{
		MediaObjectID: r.mediaObjectIDs[index], RootFolderID: r.rootFolderID,
		RelPath: r.observed[index].File.RelPath, Candidates: candidates, At: r.at,
	})
	r.outcomes[index] = AttemptQueued
}

func (r *batchResult) note(index int, pass library.AttemptPass) {
	r.passes[index] = append(r.passes[index], pass)
}

// attempts is the record of every file that needed more than its embedded tags, in batch order.
func (r *batchResult) attempts() []library.Attempt {
	var attempts []library.Attempt
	for index := range r.observed {
		passes, found := r.passes[index]
		if !found {
			continue
		}
		outcome, decided := r.outcomes[index]
		if !decided {
			outcome = AttemptRetry
		}
		attempts = append(attempts, library.Attempt{
			MediaObjectID: r.mediaObjectIDs[index], At: r.at, Outcome: outcome, Passes: passes,
		})
	}
	return attempts
}

// pendingFile is a batch file no pass has identified yet, with the best candidates seen so far.
type pendingFile struct {
	index      int
	candidates []model.Identification
}

// fingerprintPass recovers names from the audio itself and matches the folder as an album
// with them -- AcoustID identifies, the ordered metadata sources supply the data (D12).
// A single recording names many release groups (its album, its single, every compilation
// it was ever on), so asking which of them this file belongs to is unanswerable per file
// and ordinary for a folder scored against whole tracklists. With no album lookup
// configured it falls back to the per-file chain, which can answer only when AcoustID
// named exactly one.
//
// With no key configured it records that it did not run: a file skipped for a missing key
// is not a file AcoustID had no answer for, and only the record tells them apart (D12).
func (c Consumer) fingerprintPass(
	ctx context.Context, out *batchResult, identifiers []model.Identifier, files []pendingFile,
) []pendingFile {
	if c.acoustID == nil || !c.acoustID.Enabled() {
		for _, file := range files {
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeDisabled})
		}
		return files
	}
	if c.albums == nil {
		return c.runChain(ctx, out, PassFingerprint, identifiers, files)
	}

	named, locals, seeds, pending := c.namesFromAudio(ctx, out, files)
	if len(named) == 0 {
		return pending
	}
	decision, err := IdentifyAlbum(ctx, c.albums, musicbrainz.PriorityIdentify, locals, seeds)
	if err != nil {
		c.logger.Warn("album identification from fingerprints failed",
			slog.String("path", out.observed[named[0].index].File.RelPath), slog.Any("error", err))
		for _, file := range named {
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeFailed})
		}
		return append(pending, named...)
	}
	return append(pending, applyAlbumDecision(out, PassFingerprint, named, decision.Within(c.chainConfig().AmbiguityThreshold))...)
}

// namesFromAudio fingerprints every still-unidentified file in one call and asks AcoustID
// what each one is. It answers the files it could name with their recovered tracks, the
// release groups to seed recall with -- most agreed-on first, since the album every track
// names is the album the folder is -- and the files it could not name, still pending.
func (c Consumer) namesFromAudio(
	ctx context.Context, out *batchResult, files []pendingFile,
) (named []pendingFile, locals []LocalTrack, seeds []string, pending []pendingFile) {
	paths := make([]string, len(files))
	for i, file := range files {
		paths[i] = path.Join(out.rootPath, out.observed[file.index].File.RelPath)
	}
	prints, err := c.fingerprints.Fingerprint(ctx, paths)
	if err != nil || len(prints) != len(files) {
		c.logger.Warn("fingerprinting failed", slog.Any("error", err))
		for _, file := range files {
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeFailed})
		}
		return nil, nil, nil, files
	}

	recognised := make([][]acoustid.Recording, len(files))
	for i, file := range files {
		if prints[i].Err != nil || prints[i].Chromaprint == "" {
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeNone})
			continue
		}
		matches, err := c.acoustID.Lookup(ctx, prints[i].Chromaprint, prints[i].Duration)
		if err != nil {
			c.logger.Warn("acoustid lookup failed",
				slog.String("path", out.observed[file.index].File.RelPath), slog.Any("error", err))
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeFailed})
			continue
		}
		recognised[i] = namedRecordings(matches)
	}

	artist := consensusArtist(recognised)
	groupCounts := make(map[string]int)
	var groupOrder []string
	for i, file := range files {
		one := out.observed[file.index]
		recording, found := chooseRecording(recognised[i], artist, prints[i].Duration)
		if !found {
			if len(recognised[i]) == 0 && out.noted(file.index, PassFingerprint) {
				// The lookup already recorded why it answered nothing.
				pending = append(pending, file)
				continue
			}
			out.note(file.index, library.AttemptPass{Pass: PassFingerprint, Outcome: PassOutcomeNone})
			pending = append(pending, file)
			continue
		}
		named = append(named, file)
		locals = append(locals, recordedTrack(one, recording, prints[i].Duration))
		for _, group := range recording.ReleaseGroupMBIDs {
			if groupCounts[group] == 0 {
				groupOrder = append(groupOrder, group)
			}
			groupCounts[group]++
		}
	}
	slices.SortStableFunc(groupOrder, func(a, b string) int { return groupCounts[b] - groupCounts[a] })
	return named, locals, groupOrder, pending
}

// namedRecordings is every recording a lookup named, best-scoring match first.
func namedRecordings(matches []acoustid.Match) []acoustid.Recording {
	var recordings []acoustid.Recording
	for _, match := range matches {
		for _, recording := range match.Recordings {
			if recording.Title != "" {
				recordings = append(recordings, recording)
			}
		}
	}
	return recordings
}

// consensusArtist is the artist the folder agrees on: the name most of its files
// have among their own candidates, counted once per file. AcoustID's database is
// crowd-contributed and a fingerprint can carry an outright wrong link -- a real
// Heka track answered with a recording by a band called Badtown Boys, first in the
// list (measured 2026-09-18). One file cannot tell which of its candidates is the
// wrong one; a folder can.
func consensusArtist(recognised [][]acoustid.Recording) string {
	counts := make(map[string]int)
	names := make(map[string]string)
	for _, recordings := range recognised {
		seen := make(map[string]bool)
		for _, recording := range recordings {
			key := strings.ToLower(strings.TrimSpace(recording.Artist))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			counts[key]++
			names[key] = recording.Artist
		}
	}
	best, bestCount := "", 1
	for key, count := range counts {
		if count > bestCount || (count == bestCount && key < best) {
			best, bestCount = key, count
		}
	}
	return names[best]
}

// chooseRecording picks which of one file's candidates it actually is: the folder's
// own artist decides, and among that artist's recordings the one whose length is
// closest to the file's. With no consensus the closest length alone decides.
func chooseRecording(
	recordings []acoustid.Recording, artist string, duration time.Duration,
) (acoustid.Recording, bool) {
	best, found := acoustid.Recording{}, false
	for _, recording := range recordings {
		if artist != "" && !strings.EqualFold(recording.Artist, artist) {
			continue
		}
		if !found || closer(recording.Duration, best.Duration, duration) {
			best, found = recording, true
		}
	}
	if found {
		return best, true
	}
	for _, recording := range recordings {
		if !found || closer(recording.Duration, best.Duration, duration) {
			best, found = recording, true
		}
	}
	return best, found
}

// closer reports whether candidate is nearer to want than incumbent is.
func closer(candidate, incumbent, want time.Duration) bool {
	if want <= 0 {
		return false
	}
	return (candidate - want).Abs() < (incumbent - want).Abs()
}

// recordedTrack is one file as album matching sees it once its audio has been
// recognised: the names come from the recording, the length from the file itself,
// and the position from its name, since AcoustID knows neither.
func recordedTrack(one scan.Observed, recording acoustid.Recording, decoded time.Duration) LocalTrack {
	local := ReadLocalTrack(one.Probe.Tags, one.Probe.Measured, one.File.RelPath)
	// A name recovered from the audio is evidence of the same standing as a tag --
	// stronger, in truth, since a tag can say anything and the audio cannot.
	local.Title = Claim{Value: recording.Title, Provenance: ProvenanceTag}
	local.Artist = Claim{Value: recording.Artist, Provenance: ProvenanceTag}
	// The recording id is deliberately not asserted: AcoustID names every
	// recording MusicBrainz holds of one performance, so picking one of them
	// refutes every release that happens to use another -- measured against the
	// real service, which refuted the correct album for all 12 of its tracks.
	// A scan does not measure durations -- the probe job fills those in later
	// (ADR-39) -- but fpcalc just decoded the file and reports its real length,
	// which is what tells a 12-track album from the box set it also appears on.
	if local.Duration == 0 {
		local.Duration = decoded
	}
	if local.Duration == 0 {
		local.Duration = recording.Duration
	}
	return local
}

// chainConfig is the chain's thresholds, with the configured identification threshold when one
// is wired.
func (c Consumer) chainConfig() model.ChainConfig {
	chain := c.chain
	if c.threshold != nil {
		chain.AmbiguityThreshold = c.threshold()
	}
	return chain
}

// runChain runs the ADR-24 chain per pending file, answering what is still unresolved; a file
// whose strategy failed to run is dropped unqueued, so a transient failure never reads as "no candidates".
func (c Consumer) runChain(
	ctx context.Context, out *batchResult, pass string, identifiers []model.Identifier, files []pendingFile,
) []pendingFile {
	var pending []pendingFile
	for _, file := range files {
		one := out.observed[file.index]
		mediaObjectID := out.mediaObjectIDs[file.index]
		fileRef := model.FileRef{MediaObjectID: &mediaObjectID, RootFolderID: &out.rootFolderID, RelPath: one.File.RelPath}
		result, err := model.RunChain(ctx, identifiers, fileRef, c.chainConfig())
		if err != nil {
			c.logger.Warn("identify failed", slog.String("path", one.File.RelPath), slog.Any("error", err))
			out.note(file.index, library.AttemptPass{Pass: pass, Outcome: PassOutcomeFailed})
			continue
		}
		if result.Best == nil || result.Best.ExternalID == nil {
			if len(result.Candidates) > 0 {
				file.candidates = result.Candidates
			}
			out.note(file.index, library.AttemptPass{
				Pass: pass, Outcome: PassOutcomeNone, Candidates: library.AttemptCandidatesFrom(result.Candidates),
			})
			pending = append(pending, file)
			continue
		}
		if pass != PassEmbeddedTags {
			out.note(file.index, library.AttemptPass{
				Pass: pass, Outcome: PassOutcomeIdentified, Candidates: library.AttemptCandidatesFrom(result.Candidates),
			})
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
			for _, file := range group {
				out.note(file.index, library.AttemptPass{Pass: PassAlbumMatch, Outcome: PassOutcomeFailed})
			}
			pending = append(pending, group...)
			continue
		}
		pending = append(pending, applyAlbumDecision(out, PassAlbumMatch, group,
			decision.Within(c.chainConfig().AmbiguityThreshold))...)
	}
	return pending
}

// applyAlbumDecision records one album match and acts on it: identified links every
// file to its own matched track, ambiguous is queued rather than guessed per file,
// anything else stays pending for a later pass. Shared by both passes that match an
// album -- from tags, and from names recovered by fingerprinting.
func applyAlbumDecision(
	out *batchResult, pass string, group []pendingFile, decision AlbumDecision,
) []pendingFile {
	candidates := AlbumCandidates(decision)
	for _, file := range group {
		out.note(file.index, library.AttemptPass{
			Pass: pass, Outcome: string(decision.Outcome), Candidates: library.AttemptCandidatesFrom(candidates),
		})
	}
	switch decision.Outcome {
	case AlbumIdentified:
		for i, file := range group {
			out.identify(file.index, albumLink(out, file.index, *decision.Best, decision.Best.Tracks[i]),
				albumTrackFile(out, file.index, decision.Best.Tracks[i]))
		}
	case AlbumAmbiguous:
		for _, file := range group {
			out.queue(file.index, candidates)
		}
	default:
		return group
	}
	return nil
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
		Work:         matchedWorkIdentity(out.observed[index], best.Candidate.ReleaseGroup),
		RootFolderID: out.rootFolderID, MediaObjectID: out.mediaObjectIDs[index],
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
// matchedWorkIdentity names a work MusicBrainz has just identified after its own
// release group, not after the folder it happened to be in: an untagged rip lives in
// "Ripped/CD01", and calling the album that would be describing our own directory
// layout rather than the record (found live, 2026-09-18).
func matchedWorkIdentity(observed scan.Observed, group musicbrainz.ReleaseGroup) library.WorkIdentity {
	identity := newWorkIdentity(observed)
	if group.Title != "" {
		identity.CanonicalTitle, identity.SortTitle = group.Title, SortTitle(group.Title)
	}
	identity.PrimaryType = group.PrimaryType
	return identity
}

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

func (c Consumer) withoutManual(
	ctx context.Context, observed []scan.Observed, mediaObjectIDs []int64,
) ([]scan.Observed, []int64, error) {
	manual, err := c.unidentified.ManuallyIdentified(ctx, mediaObjectIDs)
	if err != nil || len(manual) == 0 {
		return observed, mediaObjectIDs, err
	}
	keptObserved := make([]scan.Observed, 0, len(observed))
	keptIDs := make([]int64, 0, len(mediaObjectIDs))
	for index, id := range mediaObjectIDs {
		if !manual[id] {
			keptObserved = append(keptObserved, observed[index])
			keptIDs = append(keptIDs, id)
		}
	}
	return keptObserved, keptIDs, nil
}

// changeFileDates is a library's "Change File Date" at rescan (Sonarr's): each file's date becomes
// its release date. Never on a root Solarr may not write to (Invariant 69); a failure is logged.
func (c Consumer) changeFileDates(root settings.RootFolder, categoryID int64, batch []scan.Observed) {
	if root.ReadOnly || !root.Writable || c.categories == nil {
		return
	}
	category, found := c.categories.Category(categoryID)
	if !found || category.FileManagement.ChangeFileDate != settings.FileDateReleaseDate {
		return
	}
	for _, observed := range batch {
		released, ok := parseReleaseDate(observed.Probe.Tags.First("date", "originaldate", "year"))
		if !ok || observed.File.ModTime.Equal(released) {
			continue
		}
		absolute := path.Join(root.Path, observed.File.RelPath)
		if err := c.fsys.Chtimes(absolute, released); err != nil {
			c.logger.Warn("file date not changed", slog.String("path", absolute), slog.Any("error", err))
		}
	}
}
