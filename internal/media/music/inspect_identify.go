package music

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// IdentifyUnit runs the ADR-24 chain over a whole unit (ADR-48 IDENTIFY) so a confident file skips costlier strategies for the rest; it needs identifiers built over UnitTagSourceFor, and the unit has no MediaObjectID or RootFolderID yet.
func IdentifyUnit(
	ctx context.Context, unit inspect.Unit, identifiers []model.Identifier, cfg model.ChainConfig,
) (model.ChainResult, error) {
	if len(unit.Media) == 0 {
		return model.ChainResult{}, nil
	}

	files := make([]model.FileRef, len(unit.Media))
	for index, media := range unit.Media {
		files[index] = model.FileRef{RelPath: media.RelPath}
	}

	result, err := model.RunChainForFiles(ctx, identifiers, files, cfg)
	if err != nil {
		return model.ChainResult{}, fmt.Errorf("music: identify unit %q: %w", unit.Directory, err)
	}
	return result, nil
}

// UnitTagSourceFor answers tag lookups for an import unit from one batch probe (ADR-28/30), like tagSourceFor but without a settings.RootFolder.
func UnitTagSourceFor(root string, probes []mediatool.Probe) Resolved {
	return Resolved{
		Tags: NewBatchTags(probes),
		Path: func(file model.FileRef) (string, bool) { return path.Join(root, file.RelPath), true },
	}
}

// RefineUnitByAlbum matches a unit its embedded MBIDs did not identify against whole
// tracklists, replacing a weaker text-search answer. probes are keyed by the unit's RelPath;
// seeds are release groups scored whatever else is recalled (a download's own album).
func RefineUnitByAlbum(
	ctx context.Context, lookup AlbumLookup, unit inspect.Unit, probes map[string]mediatool.Probe, result model.ChainResult,
	threshold float64, seeds []string,
) (model.ChainResult, *AlbumPlacement, error) {
	if lookup == nil {
		return result, nil, nil
	}
	if best := result.Best; best != nil && best.Strategy == StrategyEmbeddedTag {
		if best.ExternalID == nil || best.ExternalID.Provider != ProviderRelease {
			return result, nil, nil
		}
		release := best.ExternalID.ExternalID
		group, err := lookup.ReleaseGroupOf(ctx, musicbrainz.PriorityIdentify, release)
		if err == nil {
			return releaseAsGroup(result, release, group), nil, nil
		}
		// Keyed by its release it would become a second album beside the one keyed by its group.
		result = withoutRelease(result, release)
		if !errors.Is(err, musicbrainz.ErrNotFound) {
			return result, nil, fmt.Errorf("music: release group of unit %q: %w", unit.Directory, err)
		}
		// A release MusicBrainz does not know names no album: the tracklists decide, as for an untagged unit.
	}
	var locals []LocalTrack
	var relPaths []string
	for _, media := range unit.Media {
		if probe, found := probes[media.RelPath]; found && probe.Err == nil {
			locals = append(locals, ReadLocalTrack(probe.Tags, probe.Measured, media.RelPath))
			relPaths = append(relPaths, media.RelPath)
		}
	}
	decision, err := IdentifyAlbum(ctx, lookup, musicbrainz.PriorityIdentify, locals, Seeds{Grabbed: seeds})
	if err != nil {
		return result, nil, fmt.Errorf("music: identify unit %q as an album: %w", unit.Directory, err)
	}
	refined, placement := albumResult(decision.Within(threshold), relPaths, result)
	return refined, placement, nil
}

// releaseAsGroup keys every identification naming release by its release group, the key a followed album has.
func releaseAsGroup(result model.ChainResult, release, group string) model.ChainResult {
	rekey := func(identification model.Identification) model.Identification {
		if id := identification.ExternalID; id != nil && id.Provider == ProviderRelease && id.ExternalID == release {
			identification.ExternalID = &model.ExternalID{Provider: ProviderReleaseGroup, ExternalID: group, Confidence: id.Confidence}
		}
		return identification
	}
	out := model.ChainResult{Candidates: make([]model.Identification, len(result.Candidates))}
	for index, candidate := range result.Candidates {
		out.Candidates[index] = rekey(candidate)
	}
	if result.Best != nil {
		best := rekey(*result.Best)
		out.Best = &best
	}
	return out
}

// withoutRelease drops the identifications naming release, so neither DECIDE nor a review can add an album by it.
func withoutRelease(result model.ChainResult, release string) model.ChainResult {
	out := model.ChainResult{}
	for _, candidate := range result.Candidates {
		if id := candidate.ExternalID; id == nil || id.Provider != ProviderRelease || id.ExternalID != release {
			out.Candidates = append(out.Candidates, candidate)
		}
	}
	return out
}

// albumResult is an album decision as the chain's answer for a unit.
func albumResult(decision AlbumDecision, relPaths []string, result model.ChainResult) (model.ChainResult, *AlbumPlacement) {
	switch decision.Outcome {
	case AlbumIdentified:
		best := AlbumCandidates(AlbumDecision{Outcome: decision.Outcome, Scores: []ReleaseScore{*decision.Best}})[0]
		best.Confidence = albumConfidence(*decision.Best)
		best.ExternalID.Confidence = best.Confidence
		return model.ChainResult{Best: &best, Candidates: append([]model.Identification{best}, result.Candidates...)},
			PlacementOf(decision, relPaths)
	case AlbumAmbiguous:
		return model.ChainResult{Candidates: append(AlbumCandidates(decision), result.Candidates...)}, nil
	}
	return result, nil
}

// AudioNaming is what a unit's files are named from their audio with: fingerprints, AcoustID, and the album sources.
type AudioNaming struct {
	Fingerprints Fingerprinter
	AcoustID     AcoustIDLookup
	Albums       AlbumLookup
}

// RefineUnitByAudio is the last rung for a unit tags and names could not identify: fingerprint, match recovered names against whole tracklists seeded by AcoustID's release groups (D12); needs an AcoustID key.
func RefineUnitByAudio(
	ctx context.Context, naming AudioNaming, unit inspect.Unit, probes map[string]mediatool.Probe,
	absolute func(relPath string) string, result model.ChainResult, threshold float64, seeds []string,
) (model.ChainResult, *AlbumPlacement, error) {
	if naming.Albums == nil || naming.Fingerprints == nil || naming.AcoustID == nil || !naming.AcoustID.Enabled() ||
		result.Best != nil {
		return result, nil, nil
	}
	var relPaths, paths []string
	for _, media := range unit.Media {
		if probe, found := probes[media.RelPath]; found && probe.Err == nil {
			relPaths = append(relPaths, media.RelPath)
			paths = append(paths, absolute(media.RelPath))
		}
	}
	if len(paths) == 0 {
		return result, nil, nil
	}
	prints, err := naming.Fingerprints.Fingerprint(ctx, paths)
	if err != nil || len(prints) != len(paths) {
		return result, nil, fmt.Errorf("music: fingerprint unit %q: %w", unit.Directory, err)
	}
	recognised := make([][]acoustid.Recording, len(paths))
	for index, print := range prints {
		if print.Err != nil || print.Chromaprint == "" {
			continue
		}
		matches, err := naming.AcoustID.Lookup(ctx, print.Chromaprint, print.Duration)
		if err != nil {
			continue
		}
		recognised[index] = namedRecordings(matches)
	}
	artist := consensusArtist(recognised)
	var locals []LocalTrack
	var namedPaths []string
	counts := make(map[string]int)
	var order []string
	for index, relPath := range relPaths {
		recording, found := chooseRecording(recognised[index], artist, prints[index].Duration)
		if !found {
			continue
		}
		probe := probes[relPath]
		locals = append(locals, recordedLocal(ReadLocalTrack(probe.Tags, probe.Measured, relPath), recording, prints[index].Duration))
		namedPaths = append(namedPaths, relPath)
		for _, group := range recording.ReleaseGroupMBIDs {
			if counts[group] == 0 {
				order = append(order, group)
			}
			counts[group]++
		}
	}
	if len(locals) == 0 {
		return result, nil, nil
	}
	slices.SortStableFunc(order, func(a, b string) int { return counts[b] - counts[a] })
	decision, err := IdentifyAlbum(ctx, naming.Albums, musicbrainz.PriorityIdentify, locals, Seeds{Grabbed: seeds, Siblings: order})
	if err != nil {
		return result, nil, fmt.Errorf("music: identify unit %q from its audio: %w", unit.Directory, err)
	}
	refined, placement := albumResult(decision.Within(threshold), namedPaths, result)
	return refined, placement, nil
}
