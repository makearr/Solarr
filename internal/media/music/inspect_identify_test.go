package music_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
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

	refined, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.9, nil)
	if err != nil {
		t.Fatalf("RefineUnitByAlbum: %v", err)
	}
	if refined.Best == nil || refined.Best.ExternalID.ExternalID != "rg-studio" || placement == nil {
		t.Fatalf("Best = %+v, placement %v; want rg-studio placed", refined.Best, placement)
	}
	if refined.Best.Confidence < 0.9 || refined.Best.Precedence != model.PrecedenceMatched {
		t.Errorf("Best = %.2f at precedence %d, want >= 0.90 and matched", refined.Best.Confidence, refined.Best.Precedence)
	}

	strict, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strict.Best != nil || placement != nil || len(strict.Candidates) == 0 {
		t.Errorf("under 0.999: Best = %+v, want the match held for review as a candidate", strict.Best)
	}
}

// releaseTaggedUnit is traumaUnit whose files carry the release id and no release group id, and the chain's answer.
func releaseTaggedUnit(t *testing.T) (inspect.Unit, map[string]mediatool.Probe, *fakeAlbumLookup, model.ChainResult) {
	t.Helper()
	unit, probes, lookup := traumaUnit()
	batch := make([]mediatool.Probe, 0, len(unit.Media))
	for _, media := range unit.Media {
		probe := probes[media.RelPath]
		probe.Tags["musicbrainz_albumid"] = releaseMBID
		probes[media.RelPath] = probe
		batch = append(batch, probe)
	}
	identifiers := []model.Identifier{music.NewMBIDIdentifier(music.UnitTagSourceFor("/", batch))}
	result, err := music.IdentifyUnit(t.Context(), unit, identifiers, model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.9})
	if err != nil || result.Best == nil || result.Best.ExternalID.Provider != music.ProviderRelease {
		t.Fatalf("IdentifyUnit = %+v (%+v), %v; want the embedded release", result.Best, result.Candidates, err)
	}
	return unit, probes, lookup, result
}

// A release id alone names its album through its release group, the key a followed album has (2026-11-20:
// the import made a second album keyed by the release and the followed one stayed missing).
func TestRefineUnitByAlbum_AnEmbeddedReleaseIsResolvedToItsReleaseGroup(t *testing.T) {
	unit, probes, lookup, result := releaseTaggedUnit(t)
	lookup.groupOf = map[string]string{releaseMBID: "rg-studio"}
	other := model.Identification{
		Strategy:   music.StrategyEmbeddedTag,
		ExternalID: &model.ExternalID{Provider: music.ProviderRelease, ExternalID: "rel-other", Confidence: 1},
		Confidence: 0.5,
	}
	result.Candidates = append(result.Candidates, other)

	refined, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, result, 0.9, nil)
	if err != nil {
		t.Fatalf("RefineUnitByAlbum: %v", err)
	}
	want := model.ExternalID{Provider: music.ProviderReleaseGroup, ExternalID: "rg-studio", Confidence: 1}
	if refined.Best == nil || *refined.Best.ExternalID != want || refined.Best.Strategy != music.StrategyEmbeddedTag ||
		refined.Best.Confidence != result.Best.Confidence || placement != nil {
		t.Fatalf("Best = %+v (%+v), placement %v; want the embedded identity keyed by rg-studio", refined.Best, refined.Best.ExternalID, placement)
	}
	// A review picks from the candidates: the resolved one is rekeyed too, another release is not its to rename.
	if len(refined.Candidates) != 2 || *refined.Candidates[0].ExternalID != want || *refined.Candidates[1].ExternalID != *other.ExternalID {
		t.Errorf("candidates = %+v, want the resolved release rekeyed and the other kept", refined.Candidates)
	}
	if len(lookup.asked) != 1 || lookup.asked[0] != "group-of:"+releaseMBID {
		t.Errorf("asked %v, want only the one release lookup", lookup.asked)
	}
}

// A release MusicBrainz does not know names no album: the tracklists decide, as for an untagged unit.
func TestRefineUnitByAlbum_AnUnknownEmbeddedReleaseFallsToAlbumMatching(t *testing.T) {
	unit, probes, lookup, result := releaseTaggedUnit(t)

	refined, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, result, 0.9, nil)
	if err != nil {
		t.Fatalf("RefineUnitByAlbum: %v", err)
	}
	if refined.Best == nil || refined.Best.ExternalID.Provider != music.ProviderReleaseGroup ||
		refined.Best.ExternalID.ExternalID != "rg-studio" || placement == nil {
		t.Fatalf("Best = %+v, placement %v; want rg-studio matched by its tracklist", refined.Best, placement)
	}
}

// MusicBrainz unreachable: the unit waits rather than becoming an album keyed by its release.
func TestRefineUnitByAlbum_AnEmbeddedReleaseUnresolvedIsNotCommitted(t *testing.T) {
	unit, probes, lookup, result := releaseTaggedUnit(t)
	lookup.err = errors.New("down")

	refined, _, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, result, 0.9, nil)
	if err == nil || refined.Best != nil || len(refined.Candidates) != 0 || model.Decide(refined) != model.DecisionQueue {
		t.Fatalf("result = %+v, err %v; want no identity, no candidate to pick, and the error", refined, err)
	}
	if len(lookup.asked) != 1 {
		t.Errorf("asked %v, want no album matching against a source that just failed", lookup.asked)
	}
}

// An embedded release group is already the album's key: nothing is looked up.
func TestRefineUnitByAlbum_AnEmbeddedReleaseGroupIsKept(t *testing.T) {
	unit, probes, lookup := traumaUnit()
	best := model.Identification{
		Strategy:   music.StrategyEmbeddedTag,
		ExternalID: &model.ExternalID{Provider: music.ProviderReleaseGroup, ExternalID: "rg-studio", Confidence: 1},
		Precedence: model.PrecedenceAsserted, Confidence: 0.98,
	}
	result := model.ChainResult{Best: &best, Candidates: []model.Identification{best}}

	refined, _, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, result, 0.9, nil)
	if err != nil || refined.Best != &best || len(lookup.asked) != 0 {
		t.Errorf("Best = %+v, err %v, asked %v; want the tag kept and nothing asked", refined.Best, err, lookup.asked)
	}
}

// Section 12: a download is identified against the album it was grabbed for, even when nothing
// its files say recalls that album -- the tracklist still has to fit.
func TestRefineUnitByAlbum_ADownloadIsMatchedAgainstTheAlbumItWasGrabbedFor(t *testing.T) {
	unit, probes, lookup := traumaUnit()
	lookup.searches = nil // the names recall nothing
	lookup.groups = map[string]musicbrainz.ReleaseGroup{
		"rg-studio": {MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album"},
	}

	unseeded, _, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.9, nil)
	if err != nil {
		t.Fatal(err)
	}
	if unseeded.Best != nil {
		t.Fatalf("without provenance Best = %+v, want nothing recalled", unseeded.Best)
	}

	seeded, placement, err := music.RefineUnitByAlbum(t.Context(), lookup, unit, probes, model.ChainResult{}, 0.9, []string{"rg-studio"})
	if err != nil {
		t.Fatal(err)
	}
	if seeded.Best == nil || seeded.Best.ExternalID.ExternalID != "rg-studio" || placement == nil {
		t.Errorf("with provenance Best = %+v, placement %v; want rg-studio placed", seeded.Best, placement)
	}
}

// printsByPath fingerprints a file as its own path, so the AcoustID fake can answer per file.
type printsByPath struct{}

func (printsByPath) Fingerprint(_ context.Context, paths []string) ([]mediatool.Fingerprint, error) {
	prints := make([]mediatool.Fingerprint, len(paths))
	for index, path := range paths {
		prints[index] = mediatool.Fingerprint{Path: path, Chromaprint: path, Duration: 200 * time.Second}
	}
	return prints, nil
}

// namesByPrint answers the recording each fingerprint names.
type namesByPrint struct {
	enabled bool
	named   map[string]acoustid.Recording
}

func (n namesByPrint) Enabled() bool { return n.enabled }

func (n namesByPrint) Lookup(_ context.Context, fingerprint string, _ time.Duration) ([]acoustid.Match, error) {
	recording, found := n.named[fingerprint]
	if !found {
		return nil, nil
	}
	return []acoustid.Match{{Score: 0.97, Recordings: []acoustid.Recording{recording}}}, nil
}

// D12 on the import side: a unit whose files carry no tags at all is named from its audio and matched as an album,
// the way a scan's fingerprint pass does -- and nothing happens without an AcoustID key.
func TestRefineUnitByAudio_NamesATaglessUnitFromItsFingerprints(t *testing.T) {
	unit, probes, lookup := traumaUnit()
	lookup.searches = nil
	named := map[string]acoustid.Recording{}
	for index, track := range traumaTracks {
		relPath := fmt.Sprintf("downloads/P!po - Kebilegev Bibihi/%02d.flac", index+1)
		probe := probes[relPath]
		probe.Tags = mediatool.Tags{}
		probes[relPath] = probe
		named["/"+relPath] = acoustid.Recording{
			MBID: "rec-" + strconv.Itoa(index), Title: track.title, Artist: "P!po",
			Duration: time.Duration(track.seconds) * time.Second, ReleaseGroupMBIDs: []string{"rg-studio"},
		}
	}
	lookup.groups = map[string]musicbrainz.ReleaseGroup{"rg-studio": {MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album"}}
	absolute := func(relPath string) string { return "/" + relPath }

	naming := music.AudioNaming{Fingerprints: printsByPath{}, AcoustID: namesByPrint{enabled: true, named: named}, Albums: lookup}
	refined, placement, err := music.RefineUnitByAudio(t.Context(), naming, unit, probes, absolute, model.ChainResult{}, 0.9, nil)
	if err != nil {
		t.Fatalf("RefineUnitByAudio: %v", err)
	}
	if refined.Best == nil || refined.Best.ExternalID.ExternalID != "rg-studio" || placement == nil {
		t.Fatalf("Best = %+v, placement %v; want the unit named from its audio and placed on rg-studio", refined.Best, placement)
	}

	naming.AcoustID = namesByPrint{enabled: false, named: named}
	if off, _, err := music.RefineUnitByAudio(t.Context(), naming, unit, probes, absolute, model.ChainResult{}, 0.9, nil); err != nil || off.Best != nil {
		t.Errorf("without a key: Best = %+v, %v; want nothing", off.Best, err)
	}
}
