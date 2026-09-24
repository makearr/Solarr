package music_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/mediatool"
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

// traumaUnit is a unit of the six Kebilegev Bibihi tracks, tagged the way a
// clean rip is but carrying no embedded MBID -- the case that reaches album
// matching rather than short-circuiting on StrategyEmbeddedTag.
func traumaUnit() (inspect.Unit, map[string]mediatool.Probe, *fakeAlbumLookup) {
	unit := unitFor("downloads/P!po - Kebilegev Bibihi")
	probes := make(map[string]mediatool.Probe, len(traumaTracks))
	for i, track := range traumaTracks {
		relPath := fmt.Sprintf("downloads/P!po - Kebilegev Bibihi/%02d.flac", i+1)
		unit.Media = append(unit.Media, inspect.File{RelPath: relPath, Class: inspect.ClassMedia})
		probes[relPath] = mediatool.Probe{
			Path: "/" + relPath,
			Tags: mediatool.Tags{
				"title": track.title, "artist": "P!po", "album": "Kebilegev Bibihi",
				"track": strconv.Itoa(i + 1),
			},
			Measured: mediatool.Measured{Duration: time.Duration(track.seconds) * time.Second},
		}
	}
	lookup := &fakeAlbumLookup{
		searches: map[string][]musicbrainz.ReleaseGroupMatch{"P!po/Kebilegev Bibihi": {
			{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album"}, ArtistCredit: "P!po"},
		}},
		releases: map[string][]musicbrainz.Release{"rg-studio": {studioRelease}},
	}
	return unit, probes, lookup
}

// One threshold governs album matches too (2026-09-24): a close match is a likely-right answer
// that commits at the 0.90 default, and the same match under a stricter threshold goes to review.
// Its precedence, not a squeezed number, keeps it below any embedded tag.
func TestRefineUnitByAlbum_TheOneThresholdDecides(t *testing.T) {
	unit, probes, lookup := traumaUnit()

	refined, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.9)
	if err != nil {
		t.Fatalf("RefineUnitByAlbum: %v", err)
	}
	if refined.Best == nil || refined.Best.ExternalID.ExternalID != "rg-studio" || placement == nil {
		t.Fatalf("Best = %+v, placement %v; want rg-studio placed", refined.Best, placement)
	}
	if refined.Best.Confidence < 0.9 || refined.Best.Precedence != model.PrecedenceMatched {
		t.Errorf("Best = %.2f at precedence %d, want >= 0.90 and matched", refined.Best.Confidence, refined.Best.Precedence)
	}

	strict, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.999)
	if err != nil {
		t.Fatal(err)
	}
	if strict.Best != nil || placement != nil || len(strict.Candidates) == 0 {
		t.Errorf("under 0.999: Best = %+v, want the match held for review as a candidate", strict.Best)
	}
}
