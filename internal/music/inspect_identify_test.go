package music_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/inspect"
	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/music"
)

func unitFor(directory string, relPaths ...string) inspect.Unit {
	media := make([]inspect.File, len(relPaths))
	for i, relPath := range relPaths {
		media[i] = inspect.File{RelPath: relPath, Class: inspect.ClassMedia}
	}
	return inspect.Unit{Directory: directory, Media: media}
}

// The whole point of ADR-48's IDENTIFY step, proven against the widened FileRef: an
// import unit with no MediaObjectID/RootFolderID resolves through the exact same
// MBIDIdentifier that already identifies scanned files, and two of its three files
// agreeing on the embedded release group merges into one confident candidate even
// though the third carries no tags at all.
func TestIdentifyUnit_ResolvesFromEmbeddedTagsAcrossTheWholeUnit(t *testing.T) {
	unit := unitFor("downloads/Cibiz M - Zuroveguv Wants to Dance",
		"downloads/Cibiz M - Zuroveguv Wants to Dance/01.flac",
		"downloads/Cibiz M - Zuroveguv Wants to Dance/02.flac",
	)

	probes := []mediatool.Probe{
		{Path: "/downloads/Cibiz M - Zuroveguv Wants to Dance/01.flac",
			Tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}},
		{Path: "/downloads/Cibiz M - Zuroveguv Wants to Dance/02.flac",
			Tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}},
	}
	tags := music.UnitTagSourceFor("/", probes)
	identifiers := []model.Identifier{music.NewMBIDIdentifier(tags)}

	result, err := music.IdentifyUnit(t.Context(), unit, identifiers,
		model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.6})
	if err != nil {
		t.Fatalf("IdentifyUnit: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("got %d candidates, want the two agreeing files merged into one: %+v",
			len(result.Candidates), result.Candidates)
	}
	if result.Best == nil || result.Best.ExternalID.ExternalID != releaseGroupMBID {
		t.Errorf("Best = %+v, want %q", result.Best, releaseGroupMBID)
	}
}

// Two files that genuinely disagree must not resolve automatically -- this is what
// keeps IDENTIFY honest about a unit GROUP got structurally right but that carries
// contradictory embedded identity, exactly the case F29's queue exists for.
func TestIdentifyUnit_RefusesToResolveWhenFilesDisagree(t *testing.T) {
	unit := unitFor("downloads/mixed",
		"downloads/mixed/01.flac", "downloads/mixed/02.flac")

	probes := []mediatool.Probe{
		{Path: "/downloads/mixed/01.flac", Tags: mediatool.Tags{"musicbrainz_releasegroupid": releaseGroupMBID}},
		{Path: "/downloads/mixed/02.flac", Tags: mediatool.Tags{"musicbrainz_releasegroupid": recordingMBID}},
	}
	tags := music.UnitTagSourceFor("/", probes)
	identifiers := []model.Identifier{music.NewMBIDIdentifier(tags)}

	result, err := music.IdentifyUnit(t.Context(), unit, identifiers,
		model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.6})
	if err != nil {
		t.Fatalf("IdentifyUnit: %v", err)
	}
	if result.Best != nil {
		t.Errorf("Best = %+v, want nil: two files disagree on identity", result.Best)
	}
	if len(result.Candidates) != 2 {
		t.Errorf("got %d candidates, want both disagreeing identities kept for the queue", len(result.Candidates))
	}
}

// A unit with no media files (all artwork/sidecar) is not an error -- there is
// nothing to identify, and a caller iterating units must not have to special-case
// this itself.
func TestIdentifyUnit_EmptyMediaIsNotAnError(t *testing.T) {
	unit := inspect.Unit{Directory: "downloads/artwork-only"}

	result, err := music.IdentifyUnit(t.Context(), unit, nil, model.ChainConfig{})
	if err != nil {
		t.Fatalf("IdentifyUnit: %v", err)
	}
	if result.Best != nil || len(result.Candidates) != 0 {
		t.Errorf("result = %+v, want the zero value", result)
	}
}
