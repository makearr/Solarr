package music

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

const (
	// albumSearchLimit bounds the artist+album release group search.
	albumSearchLimit = 8
	// recordingSearchLimit bounds each track's recording search.
	recordingSearchLimit = 5
	// recordingSampleLimit tracks are searched when tags are sparse; two when they already recalled enough.
	recordingSampleLimit       = 4
	recordingSampleLimitStrong = 2
	// recordingRecallLimit release groups are kept from recording search, the most-hit first.
	recordingRecallLimit = 5
	// maxAlbumCandidates release groups are scored at most per folder.
	maxAlbumCandidates = 10
)

// Recall names how a release group came to be scored.
const (
	RecallSibling         = "sibling"
	RecallAlbumTags       = "album_tags"
	RecallRecordingSearch = "recording_search"
)

// AlbumLookup is what album identification asks the metadata sources; musicbrainz.Provider implements it.
type AlbumLookup interface {
	Search(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.ReleaseGroupMatch, error)
	SearchRecordings(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.RecordingMatch, error)
	Album(ctx context.Context, priority musicbrainz.Priority, releaseGroupMBID, releaseMBID string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error)
	Releases(ctx context.Context, priority musicbrainz.Priority, releaseGroupMBID string) ([]musicbrainz.Release, error)
}

// recalled is one release group to score, and the credit to compare the folder's artist against.
type recalled struct {
	group  musicbrainz.ReleaseGroup
	credit string
	recall string
	known  bool
}

// IdentifyAlbum recalls release groups for a folder (seeds: identified siblings'), scores every
// release of each against its files, and decides. An error means every lookup failed.
func IdentifyAlbum(
	ctx context.Context, lookup AlbumLookup, priority musicbrainz.Priority, locals []LocalTrack, seeds []string,
) (AlbumDecision, error) {
	if len(locals) == 0 {
		return AlbumDecision{Outcome: AlbumNoCandidate}, nil
	}
	var failures []error
	groups := recallAlbums(ctx, lookup, priority, locals, seeds, &failures)
	calls := len(failures)

	var candidates []AlbumCandidate
	for _, group := range groups {
		if !group.known {
			rg, _, err := lookup.Album(ctx, priority, group.group.MBID, "")
			calls++
			if err != nil {
				failures = append(failures, err)
				continue
			}
			group.group, group.credit = rg, creditNames(rg.ArtistCredit)
		}
		releases, err := lookup.Releases(ctx, priority, group.group.MBID)
		calls++
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, release := range releases {
			candidates = append(candidates, AlbumCandidate{
				ReleaseGroup: group.group, ArtistCredit: group.credit, Release: release, Recall: group.recall,
			})
		}
	}
	if len(candidates) == 0 && calls > 0 && len(failures) == calls {
		return AlbumDecision{}, fmt.Errorf("music: identify album: every lookup failed: %w", errors.Join(failures...))
	}
	return DecideAlbum(locals, candidates), nil
}

// recallAlbums gathers release groups in order of evidence: seeds, the album tags, then
// recording search when the tags alone did not recall enough.
func recallAlbums(
	ctx context.Context, lookup AlbumLookup, priority musicbrainz.Priority, locals []LocalTrack, seeds []string,
	failures *[]error,
) []recalled {
	var groups []recalled
	seen := make(map[string]bool)
	add := func(group recalled) {
		if group.group.MBID == "" || seen[group.group.MBID] || len(groups) >= maxAlbumCandidates {
			return
		}
		seen[group.group.MBID] = true
		groups = append(groups, group)
	}
	for _, seed := range seeds {
		add(recalled{group: musicbrainz.ReleaseGroup{MBID: seed}, recall: RecallSibling})
	}

	artist := consensusClaim(locals, func(t LocalTrack) Claim { return t.Artist })
	album := consensusClaim(locals, func(t LocalTrack) Claim { return t.Album })
	if artist.present() && album.present() {
		// The raw tag is searched too: "(2008 mix)" is noise to cleanAlbumTag but part of a real title.
		titles := []string{cleanAlbumTag(album.Value)}
		if raw := strings.TrimSpace(album.Value); !strings.EqualFold(raw, titles[0]) {
			titles = append(titles, raw)
		}
		for _, title := range titles {
			matches, err := lookup.Search(ctx, priority, artist.Value, title, albumSearchLimit)
			if err != nil {
				*failures = append(*failures, err)
			}
			for _, found := range matches {
				add(recalled{group: found.ReleaseGroup, credit: found.ArtistCredit, recall: RecallAlbumTags, known: true})
			}
		}
	}

	fromTags := len(groups) - len(seeds)
	if artist.present() && album.present() && fromTags >= 2 {
		return groups
	}
	sample := recordingSampleLimit
	if artist.present() && album.present() {
		sample = recordingSampleLimitStrong
	}
	for _, group := range recordingRecall(ctx, lookup, priority, locals, artist, sample, failures) {
		add(group)
	}
	return groups
}

// recordingRecall searches a few tracks' titles and keeps the release groups most of them
// appear on, studio albums before compilations.
func recordingRecall(
	ctx context.Context, lookup AlbumLookup, priority musicbrainz.Priority, locals []LocalTrack, artist Claim,
	sample int, failures *[]error,
) []recalled {
	type tally struct {
		group  musicbrainz.ReleaseGroup
		credit string
		hits   int
		first  int
	}
	tallies := make(map[string]*tally)
	searchArtist := ""
	if artist.present() {
		searchArtist = artist.Value
	}
	searched := 0
	for _, local := range locals {
		if searched >= sample {
			break
		}
		if !local.Title.present() {
			continue
		}
		searched++
		matches, err := lookup.SearchRecordings(ctx, priority, searchArtist, local.Title.Value, recordingSearchLimit)
		if err != nil {
			*failures = append(*failures, err)
			continue
		}
		hitThisTrack := make(map[string]bool)
		for _, found := range matches {
			for _, group := range found.ReleaseGroups {
				if hitThisTrack[group.MBID] {
					continue
				}
				hitThisTrack[group.MBID] = true
				if tallies[group.MBID] == nil {
					tallies[group.MBID] = &tally{group: group, credit: found.ArtistCredit, first: len(tallies)}
				}
				tallies[group.MBID].hits++
			}
		}
	}

	ranked := make([]*tally, 0, len(tallies))
	for _, one := range tallies {
		ranked = append(ranked, one)
	}
	slices.SortFunc(ranked, func(a, b *tally) int {
		return cmp.Or(
			cmp.Compare(b.hits, a.hits),
			cmp.Compare(releaseTypePenalty(a.group), releaseTypePenalty(b.group)),
			cmp.Compare(a.first, b.first),
		)
	})
	groups := make([]recalled, 0, min(len(ranked), recordingRecallLimit))
	for _, one := range ranked[:min(len(ranked), recordingRecallLimit)] {
		// Recording search carries no release group credit, so Album fills it in before scoring.
		groups = append(groups, recalled{group: one.group, credit: one.credit, recall: RecallRecordingSearch})
	}
	return groups
}

func creditNames(credits []musicbrainz.ArtistCredit) string {
	names := make([]string, len(credits))
	for i, credit := range credits {
		names[i] = credit.Name
	}
	return strings.Join(names, " & ")
}

// albumConfidence sits above text search's ceiling and below the weakest embedded MBID:
// a whole tracklist fitting is better evidence than a title, never better than an id.
func albumConfidence(distance float64) float64 {
	return max(confidenceCeiling+0.01, confidenceFromRecording-0.02-distance*0.1)
}

// maxQueuedAlbumCandidates bounds what the manual queue is shown for one folder.
const maxQueuedAlbumCandidates = 5

// AlbumCandidates is the manual queue's view of an undecided album: its best release groups,
// each with why it was not chosen, at a confidence that can never win a chain.
func AlbumCandidates(decision AlbumDecision) []model.Identification {
	var candidates []model.Identification
	seen := make(map[string]bool)
	for _, score := range decision.Scores {
		group := score.Candidate.ReleaseGroup.MBID
		if seen[group] || len(candidates) >= maxQueuedAlbumCandidates {
			continue
		}
		seen[group] = true
		confidence := candidateConfidence * max(0, 1-score.Distance)
		candidates = append(candidates, model.Identification{
			Strategy:   StrategyParse,
			ExternalID: &model.ExternalID{Provider: ProviderReleaseGroup, ExternalID: group, Confidence: confidence},
			Confidence: confidence,
			Evidence: []model.MatchEvidence{
				{Kind: "album_outcome", Detail: string(decision.Outcome)},
				{Kind: "album_release", Detail: score.Candidate.Release.MBID},
				{Kind: "album_recall", Detail: score.Candidate.Recall},
				{Kind: "album_distance", Detail: strconv.FormatFloat(score.Distance, 'f', 3, 64)},
				{Kind: "album_mapped", Detail: strconv.Itoa(score.Mapped) + "/" + strconv.Itoa(len(score.Tracks))},
				{Kind: "album_accepted", Detail: strconv.FormatBool(score.Accepted)},
				{Kind: "album_contradicted", Detail: strconv.FormatBool(score.Contradicted)},
			},
		})
	}
	return candidates
}
