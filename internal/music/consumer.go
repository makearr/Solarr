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

// DefaultAcceptThreshold and DefaultAmbiguityThreshold tune the ADR-24 chain for the
// one strategy stage 3 has. An embedded MBID is either present, at 0.95/0.85/0.70
// (identifier.go), or the file identifies as nothing yet -- there is nothing cheaper
// to stop the chain early for and nothing else registered to be ambiguous against.
const (
	DefaultAcceptThreshold    = 0.9
	DefaultAmbiguityThreshold = 0.6
)

// Consumer records what a scan found: media_object and library_path rows always,
// the work it identifies when the MBID chain resolves one, and -- when it does
// not -- an F29 manual-identification-queue entry with whatever candidates the
// chain did find. It implements scan.Consumer, direct-wired the way the rest of
// internal/music is until stage 9.
type Consumer struct {
	fsys         fsx.FS
	physical     library.Physical
	identity     library.Identity
	unidentified library.Unidentified
	clock        clock.Clock
	chain        model.ChainConfig
	logger       *slog.Logger
}

// ConsumerConfig is what a Consumer needs. Every field is required except Chain and
// Logger.
type ConsumerConfig struct {
	FS           fsx.FS
	Physical     library.Physical
	Identity     library.Identity
	Unidentified library.Unidentified
	Clock        clock.Clock
	// Chain tunes RunChain (model.ChainConfig). The zero value takes the package
	// defaults above.
	Chain  model.ChainConfig
	Logger *slog.Logger
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
		unidentified: config.Unidentified,
		clock:        config.Clock, chain: chain, logger: logger,
	}
}

// Observe implements scan.Consumer. Every file's bytes are read once, before any
// transaction opens (Invariant 2): quick hashes first, one registration transaction,
// then the chain (pure Go, no I/O) decides identity, then one more transaction records
// whatever it resolved. A failure at any point leaves nothing lost -- the next sweep
// resumes from the un-advanced cursor and finds the same, still-idempotent work to do.
func (c Consumer) Observe(ctx context.Context, root settings.RootFolder, batch []scan.Observed) error {
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

	identifiers := []model.Identifier{NewMBIDIdentifier(tagSourceFor(root, readable))}
	links, queued, resolved := c.identifyBatch(ctx, root.ID, readable, mediaObjectIDs, identifiers)
	if err := c.identity.ResolveWorks(ctx, now, links); err != nil {
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

	c.logger.Info("scan batch recorded",
		slog.Int64("root_folder_id", root.ID), slog.Int("files", len(readable)),
		slog.Int("identified", len(links)), slog.Int("queued", len(queued)))
	return nil
}

// identifyBatch runs the ADR-24 chain per file. links is what resolved and should
// be written to the library; queued is every file whose merged result did not
// clear AmbiguityThreshold, bound for the F29 manual identification queue with
// whatever candidates RunChain still found; resolved is the media_object ids that
// did identify, so a previously queued entry for one of them can be cleared.
func (c Consumer) identifyBatch(
	ctx context.Context, rootFolderID int64, observed []scan.Observed, mediaObjectIDs []int64,
	identifiers []model.Identifier,
) (links []library.WorkLink, queued []library.QueueEntry, resolved []int64) {
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
			Edition: newEditionLink(one), RootFolderID: rootFolderID,
		})
	}
	return links, queued, resolved
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
