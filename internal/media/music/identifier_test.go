package music_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// fixedTags answers with one set of tags for any file, for the cases that are about
// what the identifier concludes rather than how it finds them.
type fixedTags struct {
	tags mediatool.Tags
	err  error
}

func (f fixedTags) TagsFor(context.Context, model.FileRef) (mediatool.Tags, error) {
	return f.tags, f.err
}

func identify(t *testing.T, tags mediatool.Tags) []model.Identification {
	t.Helper()
	mediaObjectID := int64(1)
	found, err := music.NewMBIDIdentifier(fixedTags{tags: tags}).
		Identify(t.Context(), model.FileRef{MediaObjectID: &mediaObjectID, RelPath: "a.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	return found
}

// The release group is the work-level id (ADR-7), so it wins even when the edition's
// id is also present.
func TestIdentify_PrefersTheReleaseGroupOverTheRelease(t *testing.T) {
	found := identify(t, mediatool.Tags{
		"musicbrainz_releasegroupid": releaseGroupMBID,
		"musicbrainz_albumid":        releaseMBID,
		"musicbrainz_trackid":        recordingMBID,
	})
	if len(found) != 1 {
		t.Fatalf("got %d identifications, want 1", len(found))
	}
	if found[0].ExternalID.Provider != music.ProviderReleaseGroup {
		t.Errorf("provider = %q, want %q", found[0].ExternalID.Provider, music.ProviderReleaseGroup)
	}
	if found[0].ExternalID.ExternalID != releaseGroupMBID {
		t.Errorf("id = %q, want %q", found[0].ExternalID.ExternalID, releaseGroupMBID)
	}
	if found[0].Strategy != music.StrategyEmbeddedTag {
		t.Errorf("Strategy = %q, want %q", found[0].Strategy, music.StrategyEmbeddedTag)
	}
	// Every level found is evidence, even though only one is the identity: the
	// caller that writes edition and credit rows needs them.
	if len(found[0].Evidence) != 3 {
		t.Errorf("Evidence = %+v, want all three ids", found[0].Evidence)
	}
	for _, evidence := range found[0].Evidence {
		if evidence.Kind != "embedded_tag" {
			t.Errorf("evidence kind = %q, want embedded_tag", evidence.Kind)
		}
	}
}

// A release MBID names a pressing, and resolving it to the work needs a lookup, so
// it is worth less than a release group. A recording appears on many releases and is
// worth less again.
func TestIdentify_FallsBackThroughTheLevelsWithFallingConfidence(t *testing.T) {
	group := identify(t, mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID})
	release := identify(t, mediatool.Tags{"musicbrainz_albumid": releaseMBID})
	recording := identify(t, mediatool.Tags{"musicbrainz_trackid": recordingMBID})

	if release[0].ExternalID.Provider != music.ProviderRelease {
		t.Errorf("release provider = %q", release[0].ExternalID.Provider)
	}
	if recording[0].ExternalID.Provider != music.ProviderRecording {
		t.Errorf("recording provider = %q", recording[0].ExternalID.Provider)
	}
	if !(group[0].Confidence > release[0].Confidence && release[0].Confidence > recording[0].Confidence) {
		t.Errorf("confidence %.2f/%.2f/%.2f is not strictly falling",
			group[0].Confidence, release[0].Confidence, recording[0].Confidence)
	}
	// An embedded tag says what a tagger believed, which is not certainty.
	if group[0].Confidence >= 1 {
		t.Errorf("a tag was treated as certain: %.2f", group[0].Confidence)
	}
}

// Not an error: a file with no MusicBrainz identity is exactly what the later,
// more expensive strategies exist for (ADR-24).
func TestIdentify_SaysNothingRatherThanFailingWhenThereIsNoMBID(t *testing.T) {
	if found := identify(t, mediatool.Tags{"artist": "Nick Cave"}); found != nil {
		t.Errorf("got %+v for an untagged file, want nothing", found)
	}
}

// An artist id says who, never which album.
func TestIdentify_DoesNotIdentifyAWorkFromAnArtistIDAlone(t *testing.T) {
	if found := identify(t, mediatool.Tags{"musicbrainz_artistid": artistMBID}); found != nil {
		t.Errorf("got %+v from an artist id alone, want nothing", found)
	}
}

func TestIdentify_ReportsWhyItCouldNotReadTheTags(t *testing.T) {
	unreadable := errors.New("not in the probed batch")
	_, err := music.NewMBIDIdentifier(fixedTags{err: unreadable}).
		Identify(t.Context(), model.FileRef{RelPath: "a.flac"})
	if !errors.Is(err, unreadable) {
		t.Fatalf("Identify = %v, want the underlying error", err)
	}
	if !strings.Contains(err.Error(), "a.flac") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestOrder_RunsBeforeFingerprintingAndAfterAHashMatch(t *testing.T) {
	identifier := music.NewMBIDIdentifier(fixedTags{})
	// The chain sorts on Order, so the number is the contract, not a comment.
	if identifier.Order() != music.OrderEmbeddedTag {
		t.Errorf("Order = %d, want %d", identifier.Order(), music.OrderEmbeddedTag)
	}
	if music.OrderEmbeddedTag <= 0 {
		t.Error("nothing can be ordered before an embedded tag, so a hash match cannot precede it")
	}
}

// The whole point of ADR-29: the library is usable in the time it takes to read
// tags, so the identifier runs off a batch that was probed in one pass.
func TestResolved_AnswersFromTheBatchAndSaysWhenAFileWasNotInIt(t *testing.T) {
	probes := []mediatool.Probe{
		{Path: "/music/a.flac", Tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}},
		{Path: "/music/broken.m4a", Err: errors.New("moov atom not found")},
	}
	rootFolderID := int64(1)
	resolved := music.Resolved{
		Tags: music.NewBatchTags(probes),
		Path: func(file model.FileRef) (string, bool) {
			if file.RootFolderID == nil || *file.RootFolderID != 1 {
				return "", false
			}
			return "/music/" + file.RelPath, true
		},
	}
	identifier := music.NewMBIDIdentifier(resolved)

	found, err := identifier.Identify(t.Context(), model.FileRef{RootFolderID: &rootFolderID, RelPath: "a.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 1 || found[0].ExternalID.ExternalID != releaseGroupMBID {
		t.Fatalf("got %+v, want the batch's release group", found)
	}

	// A file that failed to probe is absent, not present-and-empty: identifying it
	// as "no MBID" would silently downgrade a read failure into a fact about the file.
	if _, err := identifier.Identify(t.Context(), model.FileRef{RootFolderID: &rootFolderID, RelPath: "broken.m4a"}); err == nil {
		t.Error("a file that failed to probe was treated as having no tags")
	}
	otherRootFolderID := int64(9)
	if _, err := identifier.Identify(t.Context(), model.FileRef{RootFolderID: &otherRootFolderID, RelPath: "a.flac"}); err == nil {
		t.Error("a file under an unknown root resolved anyway")
	}
	// ADR-48: an import-inspection unit has no root folder at all yet -- nil, not a
	// sentinel id, and a caller's PathOf is free to refuse it (as this one does) or
	// to resolve it against an import base instead.
	if _, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a.flac"}); err == nil {
		t.Error("a file with no root folder resolved through a PathOf that requires one")
	}
}

// ADR-48's import inspection identifies a unit before it has a root folder at
// all -- proving the one thing the widening exists for: a PathOf that resolves
// a nil RootFolderID against an import base, not just a caller that refuses it.
func TestResolved_AnswersWithNoRootFolderForAnImportUnit(t *testing.T) {
	probes := []mediatool.Probe{
		{Path: "/downloads/Unsorted/a.flac",
			Tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}},
	}
	resolved := music.Resolved{
		Tags: music.NewBatchTags(probes),
		Path: func(file model.FileRef) (string, bool) {
			if file.RootFolderID != nil {
				return "", false
			}
			return "/downloads/" + file.RelPath, true
		},
	}
	identifier := music.NewMBIDIdentifier(resolved)

	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "Unsorted/a.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 1 || found[0].ExternalID.ExternalID != releaseGroupMBID {
		t.Fatalf("got %+v, want the batch's release group", found)
	}
}

// RunChain is what actually consumes this, so the strategy is exercised through it.
func TestIdentify_ResolvesThroughTheChain(t *testing.T) {
	identifier := music.NewMBIDIdentifier(fixedTags{
		tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID},
	})
	result, err := model.RunChain(t.Context(), []model.Identifier{identifier},
		model.FileRef{RelPath: "a.flac"},
		model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.6})
	if err != nil {
		t.Fatalf("RunChain: %v", err)
	}
	if result.Best == nil {
		t.Fatal("the chain resolved nothing from an embedded release group id")
	}
	if result.Best.ExternalID.ExternalID != releaseGroupMBID {
		t.Errorf("Best = %+v, want the release group", result.Best)
	}
}
