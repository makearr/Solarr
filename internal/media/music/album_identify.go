package music

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
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
	// names are the credited artists' own names, empty when the recall did not say.
	names  string
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
	groupOfRelease := make(map[string]string)
	for _, group := range groups {
		if !group.known {
			rg, _, err := lookup.Album(ctx, priority, group.group.MBID, "")
			calls++
			if err != nil {
				failures = append(failures, err)
				continue
			}
			group.group, group.credit, group.names = rg, creditNames(rg.ArtistCredit), artistNames(rg.ArtistCredit)
		}
		releases, err := lookup.Releases(ctx, priority, group.group.MBID)
		calls++
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if earlier, merged := mergedGroup(groupOfRelease, releases); merged {
			calls++
			candidates = keepCurrentGroup(ctx, lookup, priority, candidates, earlier, group, &failures)
			continue
		}
		for _, release := range releases {
			groupOfRelease[release.MBID] = group.group.MBID
			candidates = append(candidates, AlbumCandidate{
				ReleaseGroup: group.group, ArtistCredit: group.credit, ArtistNames: group.names,
				Release: release, Recall: group.recall,
			})
		}
	}
	if len(candidates) == 0 && calls > 0 && len(failures) == calls {
		return AlbumDecision{}, fmt.Errorf("music: identify album: every lookup failed: %w", errors.Join(failures...))
	}
	return DecideAlbum(locals, candidates), nil
}

// mergedGroup answers the group already recalled with one of these releases: a release has one
// group, so sharing one means a merged group the search index still lists under its old id.
func mergedGroup(groupOfRelease map[string]string, releases []musicbrainz.Release) (string, bool) {
	for _, release := range releases {
		if earlier, found := groupOfRelease[release.MBID]; found {
			return earlier, true
		}
	}
	return "", false
}

// keepCurrentGroup scores a merged group once, under the id a lookup of the later one answers:
// MusicBrainz answers an old id with the group it was merged into.
func keepCurrentGroup(
	ctx context.Context, lookup AlbumLookup, priority musicbrainz.Priority, candidates []AlbumCandidate,
	earlier string, later recalled, failures *[]error,
) []AlbumCandidate {
	current, _, err := lookup.Album(ctx, priority, later.group.MBID, "")
	if err != nil {
		*failures = append(*failures, err)
		return candidates
	}
	if current.MBID != later.group.MBID {
		return candidates
	}
	for i := range candidates {
		if candidates[i].ReleaseGroup.MBID == earlier {
			candidates[i].ReleaseGroup, candidates[i].ArtistCredit = current, creditNames(current.ArtistCredit)
			if names := artistNames(current.ArtistCredit); names != "" {
				candidates[i].ArtistNames = names
			}
		}
	}
	return candidates
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
	search := func(title string) {
		matches, err := lookup.Search(ctx, priority, artist.Value, title, albumSearchLimit)
		if err != nil {
			*failures = append(*failures, err)
		}
		for _, found := range matches {
			add(recalled{
				group: found.ReleaseGroup, credit: found.ArtistCredit, names: found.ArtistNames,
				recall: RecallAlbumTags, known: true,
			})
		}
	}
	var titles []string
	if artist.present() && album.present() {
		// The raw tag is searched too: "(2008 mix)" is noise to cleanAlbumTag but part of a real title.
		titles = []string{cleanAlbumTag(album.Value)}
		if raw := strings.TrimSpace(album.Value); !strings.EqualFold(raw, titles[0]) {
			titles = append(titles, raw)
		}
		for _, title := range titles {
			search(title)
		}
	}

	tagsRecalledEnough := artist.present() && album.present() && len(groups)-len(seeds) >= 2
	if !tagsRecalledEnough {
		sample := recordingSampleLimit
		if artist.present() && album.present() {
			sample = recordingSampleLimitStrong
		}
		for _, group := range recordingRecall(ctx, lookup, priority, locals, artist, sample, failures) {
			add(group)
		}
	}

	// A malformed tag ("Dee Gees / Sohe Basen - Gus Hogecuru / Live") hides the title the folder
	// names; searched last and never counted as the tags' recall, since a folder name misreads
	// easily ("Mazotuz - The Remixes!" parses as artist "Runaway").
	if folder := cleanAlbumTag(folderAlbum(locals)); artist.present() && folder != "" &&
		!slices.ContainsFunc(titles, func(title string) bool { return strings.EqualFold(title, folder) }) {
		search(folder)
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

// artistNames joins the credited artists' own names, empty when the source did not give them.
func artistNames(credits []musicbrainz.ArtistCredit) string {
	var names []string
	for _, credit := range credits {
		if credit.ArtistName != "" {
			names = append(names, credit.ArtistName)
		}
	}
	return strings.Join(names, " & ")
}

// albumConfidence is a tracklist fit's likelihood: 120 of 120 right on the test library
// (2026-09-17). At the accept distance (0.20) it reaches 0.89, just short of review-free.
// It reads the gate distance: release type ranks editions, it is no evidence against one, and
// counting it sent a correct compilation to review (the main library, 2026-09-25).
func albumConfidence(score ReleaseScore) float64 {
	return 0.99 - score.GateDistance*0.5
}

// Within keeps an identified album only when it is at least threshold likely right; below it the
// match goes to review like an ambiguous one -- the one threshold governs album matching too.
func (d AlbumDecision) Within(threshold float64) AlbumDecision {
	if d.Outcome == AlbumIdentified && d.Best != nil && albumConfidence(*d.Best) < threshold {
		d.Outcome = AlbumAmbiguous
	}
	return d
}

// maxQueuedAlbumCandidates bounds what the manual queue is shown for one folder.
const maxQueuedAlbumCandidates = 5

// AlbumCandidates is a decision's best release groups, each with the facts its verdict rests on,
// at a confidence that can never win a chain; the chosen one comes first when there is one.
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
			Precedence: model.PrecedenceMatched,
			Confidence: confidence,
			Evidence:   albumEvidence(decision.Outcome, score),
		})
	}
	return candidates
}

// Evidence kinds a candidate carries, shared by every pass so a manual decision reads them alike.
const (
	EvidenceTitle          = "title"
	EvidenceArtist         = "artist"
	EvidenceReleaseType    = "release_type"
	EvidenceEdition        = "edition"
	EvidenceEditionTitle   = "edition_title"
	EvidenceEditionDate    = "edition_date"
	EvidenceEditionCountry = "edition_country"
	EvidenceEditionStatus  = "edition_status"
	EvidenceEditionTracks  = "edition_tracks"
	EvidenceFilesMatched   = "files_matched"
	EvidenceFilesSupported = "files_supported"
	EvidenceDistance       = "distance"
	EvidenceAccepted       = "accepted"
	EvidenceContradicted   = "contradicted"
	EvidenceRecall         = "recall"
	EvidenceOutcome        = "outcome"
)

func albumEvidence(outcome AlbumOutcome, score ReleaseScore) []model.MatchEvidence {
	group, release := score.Candidate.ReleaseGroup, score.Candidate.Release
	releaseType := group.PrimaryType
	for _, secondary := range group.SecondaryTypes {
		releaseType += " + " + secondary
	}
	return []model.MatchEvidence{
		{Kind: EvidenceOutcome, Detail: string(outcome)},
		{Kind: EvidenceTitle, Detail: group.Title},
		{Kind: EvidenceArtist, Detail: score.Candidate.ArtistCredit},
		{Kind: EvidenceReleaseType, Detail: releaseType},
		{Kind: EvidenceEdition, Detail: release.MBID},
		{Kind: EvidenceEditionTitle, Detail: release.Title},
		{Kind: EvidenceEditionDate, Detail: release.Date},
		{Kind: EvidenceEditionCountry, Detail: release.Country},
		{Kind: EvidenceEditionStatus, Detail: release.Status},
		{Kind: EvidenceEditionTracks, Detail: strconv.Itoa(trackCount(release))},
		{Kind: EvidenceFilesMatched, Detail: strconv.Itoa(score.Mapped) + "/" + strconv.Itoa(len(score.Tracks))},
		{Kind: EvidenceFilesSupported, Detail: strconv.Itoa(score.Supported)},
		{Kind: EvidenceDistance, Detail: strconv.FormatFloat(score.Distance, 'f', 3, 64)},
		{Kind: EvidenceAccepted, Detail: strconv.FormatBool(score.Accepted)},
		{Kind: EvidenceContradicted, Detail: strconv.FormatBool(score.Contradicted)},
		{Kind: EvidenceRecall, Detail: score.Candidate.Recall},
	}
}
