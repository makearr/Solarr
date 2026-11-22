package music

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// StrategyParse is this strategy's name in Identification.Strategy, ADR-24's fifth
// of six: hash, tag, fingerprint, xml, **parse**, provenance.
const StrategyParse = "parse"

// OrderParseSearch puts this after the embedded-tag strategy and after every
// cheaper, network-free strategy that will eventually exist (fingerprint, xml) --
// it is the first strategy in the chain that makes a network call.
const OrderParseSearch = 50

// searchResultLimit bounds one MusicBrainz search -- enough candidates for
// match.Rank to have something to disambiguate against, not so many the response
// is dominated by tail noise.
const searchResultLimit = 10

// searchConfidence is a clean text match's likelihood: 105 of 105 right on the test library
// (2026-09-17); every 0.1 of distance costs 0.05, so only a close match clears 0.90 on its own.
const searchConfidence = 0.95

// tieBreakConfidence caps a match chosen between candidates the text could not tell apart: it commits, but ranks
// below an untied match.
const tieBreakConfidence = 0.90

// candidateConfidence is the low, never-auto-resolving confidence given to every
// ranked candidate when match.Rank did not clear both its thresholds -- still
// worth keeping (F29's manual queue shows them), just never worth guessing from.
const candidateConfidence = 0.3

// DefaultSearchThresholds is section 10.2's two-gate acceptance: a clean match against one strong result resolves, anything ambiguous goes to the manual queue.
var DefaultSearchThresholds = match.Thresholds{MaxDistance: 0.3, AmbiguityMargin: 0.08}

// Searcher is what SearchIdentifier needs from a metadata provider -- exactly
// musicbrainz.Provider's own Search method signature, kept as its own interface so
// a test can fake it without building a whole Provider.
type Searcher interface {
	Search(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.ReleaseGroupMatch, error)
}

// SearchIdentifier is ADR-24's parse strategy for files without MusicBrainz ids (most scene music): claimed artist/album from tags, else the folder name (ADR-41), searched and weighed with identify/match.
type SearchIdentifier struct {
	tags       TagSource
	search     Searcher
	thresholds match.Thresholds
}

// SearchIdentifierConfig is what a SearchIdentifier needs. Thresholds' zero value
// takes DefaultSearchThresholds.
type SearchIdentifierConfig struct {
	Tags       TagSource
	Search     Searcher
	Thresholds match.Thresholds
}

// NewSearchIdentifier builds it.
func NewSearchIdentifier(config SearchIdentifierConfig) SearchIdentifier {
	thresholds := config.Thresholds
	if thresholds == (match.Thresholds{}) {
		thresholds = DefaultSearchThresholds
	}
	return SearchIdentifier{tags: config.Tags, search: config.Search, thresholds: thresholds}
}

// Name identifies the strategy in a Decision.
func (SearchIdentifier) Name() string { return "musicbrainz-search" }

// Order is OrderParseSearch.
func (SearchIdentifier) Order() int { return OrderParseSearch }

// Identify ranks MusicBrainz search results; a failed or unconfigured search counts as found nothing, since a later strategy or sweep may do better.
func (s SearchIdentifier) Identify(ctx context.Context, file model.FileRef) ([]model.Identification, error) {
	tags, err := s.tags.TagsFor(ctx, file)
	if err != nil {
		return nil, fmt.Errorf("music: read tags for %s: %w", file.RelPath, err)
	}

	artist, album := claimedIdentity(tags, file.RelPath)
	if artist == "" || album == "" {
		return nil, nil
	}

	matches, err := s.search.Search(ctx, musicbrainz.PriorityIdentify, artist, album, searchResultLimit)
	if err != nil {
		// An outage is not an answer: the caller retries rather than recording "no candidates".
		return nil, fmt.Errorf("music: search release groups for %q: %w", combinedTitle(artist, album), err)
	}
	if len(matches) == 0 {
		return nil, nil
	}

	// Year is left out of the comparison: a release group's date is the original year while a file's DATE is usually its pressing's, and year belongs to an edition (ADR-7).
	claimed := match.Claimed{Identity: match.Identity{Title: combinedTitle(artist, album)}}
	candidates := make([]match.Candidate, len(matches))
	for index, one := range matches {
		candidates[index] = match.Candidate{
			ID:       one.MBID,
			Identity: match.Identity{Title: combinedTitle(one.ArtistCredit, one.Title)},
		}
	}

	result := match.Rank(claimed, candidates, nil, s.thresholds)
	tieStep := ""
	if result.Best == nil {
		result.Best, tieStep = s.breakTie(result, matches, tags)
	}
	identifications := identificationsFor(result)
	for i, identification := range identifications {
		for _, one := range matches {
			if one.MBID == identification.ExternalID.ExternalID {
				if tieStep != "" && result.Best != nil && one.MBID == result.Best.ID {
					identifications[i].Confidence = min(identifications[i].Confidence, tieBreakConfidence)
					identifications[i].ExternalID.Confidence = identifications[i].Confidence
					identifications[i].Evidence = append(identifications[i].Evidence, model.MatchEvidence{Kind: EvidenceTieBreak, Detail: tieStep})
				}
				identifications[i].Evidence = append(identifications[i].Evidence,
					model.MatchEvidence{Kind: EvidenceTitle, Detail: one.Title},
					model.MatchEvidence{Kind: EvidenceArtist, Detail: one.ArtistCredit},
					model.MatchEvidence{Kind: EvidenceReleaseType, Detail: one.PrimaryType})
				break
			}
		}
	}
	return identifications, nil
}

// breakTie names the winner among leading candidates that share one artist and title (an album and its title single, a
// self-titled album and EP): the better release type decides, then a first-release year equal to the file's year tag.
// A tie that stays is left ambiguous. The second result names the deciding step.
func (s SearchIdentifier) breakTie(result match.Result, matches []musicbrainz.ReleaseGroupMatch, tags mediatool.Tags) (*match.RankedCandidate, string) {
	if len(result.Candidates) < 2 || result.Candidates[0].Distance.Total > s.thresholds.MaxDistance {
		return nil, ""
	}
	byID := make(map[string]musicbrainz.ReleaseGroupMatch, len(matches))
	for _, one := range matches {
		byID[one.MBID] = one
	}
	lead, found := byID[result.Candidates[0].ID]
	if !found {
		return nil, ""
	}
	var tied []int
	for i, candidate := range result.Candidates {
		one, known := byID[candidate.ID]
		if candidate.Distance.Total-result.Candidates[0].Distance.Total > s.thresholds.AmbiguityMargin {
			break
		}
		if !known || !strings.EqualFold(one.ArtistCredit, lead.ArtistCredit) || !strings.EqualFold(one.Title, lead.Title) {
			return nil, ""
		}
		tied = append(tied, i)
	}
	best := slices.MinFunc(tied, func(a, b int) int {
		return cmp.Compare(releaseTypePenalty(byID[result.Candidates[a].ID].ReleaseGroup), releaseTypePenalty(byID[result.Candidates[b].ID].ReleaseGroup))
	})
	bestPenalty := releaseTypePenalty(byID[result.Candidates[best].ID].ReleaseGroup)
	var byType []int
	for _, i := range tied {
		if releaseTypePenalty(byID[result.Candidates[i].ID].ReleaseGroup) == bestPenalty {
			byType = append(byType, i)
		}
	}
	if len(byType) == 1 {
		return &result.Candidates[byType[0]], "release_type"
	}
	year := fileYear(tags)
	if year == "" {
		return nil, ""
	}
	var byYear []int
	for _, i := range byType {
		if strings.HasPrefix(byID[result.Candidates[i].ID].FirstReleaseDate, year) {
			byYear = append(byYear, i)
		}
	}
	if len(byYear) == 1 {
		return &result.Candidates[byYear[0]], "year"
	}
	return nil, ""
}

// fileYear is the year a file's date tags give, "" when none reads as one.
func fileYear(tags mediatool.Tags) string {
	date := tags.First("originalyear", "originaldate", "date", "year")
	if len(date) >= 4 && date[0] >= '1' && date[0] <= '2' && strings.Trim(date[:4], "0123456789") == "" {
		return date[:4]
	}
	return ""
}

// identificationsFor turns a match.Result into chain input: the confident winner at an auto-resolvable confidence, other candidates low enough to only feed the manual queue (F29).
func identificationsFor(result match.Result) []model.Identification {
	identifications := make([]model.Identification, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		confidence := candidateConfidence * (1 - candidate.Distance.Total)
		if result.Best != nil && candidate.ID == result.Best.ID {
			// Both of match.Rank's gates passed, so scale only slightly by distance to stay above DefaultAmbiguityThreshold.
			confidence = searchConfidence - candidate.Distance.Total*0.5
		}
		identifications = append(identifications, model.Identification{
			Strategy:   StrategyParse,
			ExternalID: &model.ExternalID{Provider: ProviderReleaseGroup, ExternalID: candidate.ID, Confidence: confidence},
			Precedence: model.PrecedenceSearched,
			Confidence: confidence,
			Evidence:   evidenceForDistance(candidate.Distance),
		})
	}
	return identifications
}

// evidenceForDistance renders match.Distance as structured evidence (ADR-10):
// data, never a formatted verdict.
func evidenceForDistance(distance match.Distance) []model.MatchEvidence {
	evidence := make([]model.MatchEvidence, 0, len(distance.Parts))
	for _, part := range distance.Parts {
		evidence = append(evidence, model.MatchEvidence{
			Kind: "search_match_" + part.Dimension, Detail: part.Detail,
		})
	}
	return evidence
}

// claimedIdentity is what the file says it is: ARTIST/ALBUM/DATE tags when present, else the release-folder name (ADR-41).
func claimedIdentity(tags mediatool.Tags, relPath string) (artist, album string) {
	artist = tags.First("albumartist", "album_artist", "artist")
	album = tags.First("album")
	if artist != "" && album != "" {
		return artist, cleanAlbumTag(album)
	}

	parsed := parse.Parse(albumTitleFallback(relPath)).Identity
	if artist == "" {
		artist = parsed.Artist
	}
	if album == "" {
		album = parsed.Album
	}
	return artist, album
}

// cleanAlbumTag strips edition and technical noise baked into the ALBUM tag, which MusicBrainz exact-phrase search treats as a different title; it reuses identify/parse and falls back to the raw tag if cleaning empties it.
func cleanAlbumTag(album string) string {
	if cleaned := parse.Parse(album).Identity.Album; cleaned != "" {
		return cleaned
	}
	return album
}

// combinedTitle folds artist and album into one string because match's Identity has no artist dimension.
func combinedTitle(artist, title string) string {
	if artist == "" {
		return title
	}
	return artist + " - " + title
}
