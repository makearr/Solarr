package music

import (
	"context"
	"fmt"

	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/model"
)

// StrategyEmbeddedTag is this strategy's name in Identification.Strategy, one of the
// six ADR-24 names.
const StrategyEmbeddedTag = "embedded-tag"

// OrderEmbeddedTag puts this second in the chain, after a hash match on a file the
// library has already seen and before anything that reads bytes or calls out. It is
// the cheapest strategy that can identify a file the library has never seen.
const OrderEmbeddedTag = 20

// Confidence in an embedded MBID. It is high but not certain: the tag says what a
// tagger believed, and a mis-tagged file is a real thing. Reserving the top of the
// range keeps a hash match on a file we have already identified strictly better.
const (
	// confidenceReleaseGroup is a work identified directly by its own MBID.
	confidenceReleaseGroup = 0.95
	// confidenceFromRelease is a work inferred from the edition's MBID, which needs
	// a MusicBrainz lookup to resolve and so is worth less on its own.
	confidenceFromRelease = 0.85
	// confidenceFromRecording is the weakest: a recording appears on many releases.
	confidenceFromRecording = 0.70
)

// TagSource supplies the tags for a file. It exists so the chain never spawns a
// subprocess per file: a scan reads a whole batch through mediatool in one call and
// answers from that (ADR-28, ADR-30).
type TagSource interface {
	TagsFor(ctx context.Context, file model.FileRef) (mediatool.Tags, error)
}

// MBIDIdentifier identifies a music file from the MusicBrainz ids it already
// carries, with **no network call** -- ADR-29's central claim, and what makes the
// library usable in the time it takes to read tags rather than in fourteen hours.
type MBIDIdentifier struct{ tags TagSource }

// NewMBIDIdentifier builds it over a source of already-read tags.
func NewMBIDIdentifier(tags TagSource) MBIDIdentifier { return MBIDIdentifier{tags: tags} }

// Name identifies the strategy in a Decision.
func (MBIDIdentifier) Name() string { return "musicbrainz-embedded-tag" }

// Order is OrderEmbeddedTag.
func (MBIDIdentifier) Order() int { return OrderEmbeddedTag }

// Identify answers what work this file belongs to, preferring the release group,
// which is the work-level id (ADR-7). The edition, recording and artist ids are
// carried as evidence: the chain decides one identity, and the caller that writes
// edition and credit rows reads them from ReadMBIDs directly.
func (m MBIDIdentifier) Identify(ctx context.Context, file model.FileRef) ([]model.Identification, error) {
	tags, err := m.tags.TagsFor(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("music: read tags for %s: %w", file.RelPath, err)
	}

	mbids := ReadMBIDs(tags)
	if mbids.Empty() {
		// Not an error: a file with no MusicBrainz identity is exactly what the
		// later, more expensive strategies exist for.
		return nil, nil
	}

	provider, external, confidence := workIdentity(mbids)
	if external == "" {
		// Artist ids alone say who, never which album. That is evidence for a
		// later strategy, not an identity.
		return nil, nil
	}

	return []model.Identification{{
		Strategy:   StrategyEmbeddedTag,
		ExternalID: &model.ExternalID{Provider: provider, ExternalID: external, Confidence: 1.0},
		Confidence: confidence,
		Evidence:   evidenceFor(mbids),
	}}, nil
}

// workIdentity picks which MBID answers "what work is this", in the order of how
// directly each one says so.
func workIdentity(mbids MBIDs) (provider, external string, confidence float64) {
	switch {
	case mbids.ReleaseGroup != "":
		return ProviderReleaseGroup, mbids.ReleaseGroup, confidenceReleaseGroup
	case mbids.Release != "":
		return ProviderRelease, mbids.Release, confidenceFromRelease
	case mbids.Recording != "":
		return ProviderRecording, mbids.Recording, confidenceFromRecording
	}
	return "", "", 0
}

// evidenceFor is structured, never a formatted sentence (ADR-10), so F1's "why?" can
// render it and a translation never touches this file.
func evidenceFor(mbids MBIDs) []model.MatchEvidence {
	var evidence []model.MatchEvidence
	add := func(tag, value string) {
		if value != "" {
			evidence = append(evidence, model.MatchEvidence{
				Kind: "embedded_tag", Detail: tag + "=" + value,
			})
		}
	}
	add("MUSICBRAINZ_RELEASEGROUPID", mbids.ReleaseGroup)
	add("MUSICBRAINZ_ALBUMID", mbids.Release)
	add("MUSICBRAINZ_TRACKID", mbids.Recording)
	for _, artist := range mbids.AlbumArtists {
		add("MUSICBRAINZ_ALBUMARTISTID", artist)
	}
	for _, artist := range mbids.Artists {
		add("MUSICBRAINZ_ARTISTID", artist)
	}
	return evidence
}

// BatchTags is a TagSource over tags already read for a batch of files, which is how
// a scan keeps one ffprobe invocation per file instead of one per chain run.
type BatchTags struct{ byPath map[string]mediatool.Tags }

// NewBatchTags builds one from a probed batch, keyed by the path each was asked for.
func NewBatchTags(probes []mediatool.Probe) *BatchTags {
	byPath := make(map[string]mediatool.Tags, len(probes))
	for _, probe := range probes {
		// A file that failed to probe carries no tags, and is absent rather than
		// present-and-empty so TagsFor can say which it was.
		if probe.Err == nil {
			byPath[probe.Path] = probe.Tags
		}
	}
	return &BatchTags{byPath: byPath}
}

// PathOf is how a FileRef becomes the absolute path the batch was probed under.
// library.db stores a root folder id and a relative path; the roots are settings.db
// policy (ADR-25), so the caller supplies the mapping.
type PathOf func(file model.FileRef) (string, bool)

// Resolved pairs a batch of tags with the path mapping needed to look one up.
type Resolved struct {
	Tags *BatchTags
	Path PathOf
}

// TagsFor returns the tags read for this file in the batch.
func (r Resolved) TagsFor(_ context.Context, file model.FileRef) (mediatool.Tags, error) {
	path, ok := r.Path(file)
	if !ok {
		return nil, fmt.Errorf("music: no path for root folder %d and %q",
			file.RootFolderID, file.RelPath)
	}
	tags, ok := r.Tags.byPath[path]
	if !ok {
		return nil, fmt.Errorf("music: %s was not in the probed batch", path)
	}
	return tags, nil
}
