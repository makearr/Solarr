package music

import (
	"context"
	"fmt"

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

// confidenceCeiling keeps a search match strictly below MBIDIdentifier's weakest
// confidence (confidenceFromRecording, 0.70): a fuzzy text match against a search
// result is never as trustworthy as a MusicBrainz id the file itself carries, no
// matter how clean the match.
const confidenceCeiling = 0.65

// candidateConfidence is the low, never-auto-resolving confidence given to every
// ranked candidate when match.Rank did not clear both its thresholds -- still
// worth keeping (F29's manual queue shows them), just never worth guessing from.
const candidateConfidence = 0.3

// DefaultSearchThresholds is section 10.2's two-gate acceptance, tuned so a clean
// artist+album title match against a single strong search result resolves
// automatically while anything genuinely ambiguous falls through to the manual
// identification queue instead of guessing.
var DefaultSearchThresholds = match.Thresholds{MaxDistance: 0.3, AmbiguityMargin: 0.08}

// Searcher is what SearchIdentifier needs from a metadata provider -- exactly
// musicbrainz.Provider's own Search method signature, kept as its own interface so
// a test can fake it without building a whole Provider.
type Searcher interface {
	Search(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.ReleaseGroupMatch, error)
}

// SearchIdentifier is ADR-24's "parse" strategy: read what artist/album a file
// itself claims (tags first, the release-folder name as fallback -- ADR-41's path
// evidence), search MusicBrainz for release groups, and weigh what comes back with
// internal/identify/match. It exists for the files MBIDIdentifier cannot answer at all: a
// file with no embedded MusicBrainz ids, which is most scene-released music.
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

// Identify searches MusicBrainz for the file's claimed artist/album and ranks
// what comes back. A search failure, or a search with no source configured to
// answer it at all, is treated as "found nothing" rather than an error -- the
// same reasoning as MBIDIdentifier's empty-tags case: it says nothing about
// whether a later strategy or a later sweep can do better.
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
		// Deliberately swallowed, not propagated: a search failure says nothing
		// about whether a later strategy or a later sweep can do better, the same
		// reasoning as MBIDIdentifier's empty-tags case just above.
		return nil, nil //nolint:nilerr
	}
	if len(matches) == 0 {
		return nil, nil
	}

	// Year is deliberately left out of this comparison, proven against real scene
	// rips rather than assumed: a release group's FirstReleaseDate is the
	// *original* release year, while a file's own DATE tag (or a year parsed from
	// the release folder name) is usually the year of *this particular* pressing
	// -- a remaster or WEB reissue tagged 2018 for a 1978 album, say. Comparing
	// them as if they were the same field turned a perfect title match into a
	// rejected one. Year belongs to an edition (ADR-7), not a release group, and
	// this identifier only ever searches at release-group (work) level.
	claimed := match.Claimed{Identity: match.Identity{Title: combinedTitle(artist, album)}}
	candidates := make([]match.Candidate, len(matches))
	for index, one := range matches {
		candidates[index] = match.Candidate{
			ID:       one.MBID,
			Identity: match.Identity{Title: combinedTitle(one.ArtistCredit, one.Title)},
		}
	}

	result := match.Rank(claimed, candidates, nil, s.thresholds)
	identifications := identificationsFor(result)
	for i, identification := range identifications {
		for _, one := range matches {
			if one.MBID == identification.ExternalID.ExternalID {
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

// identificationsFor turns a match.Result into RunChain's input: the confident
// winner (if any) at a real, chain-auto-resolvable confidence, every other ranked
// candidate at a low confidence that can only ever populate F29's manual queue,
// never win the chain outright.
func identificationsFor(result match.Result) []model.Identification {
	identifications := make([]model.Identification, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		confidence := candidateConfidence * (1 - candidate.Distance.Total)
		if result.Best != nil && candidate.ID == result.Best.ID {
			// Both of match.Rank's own gates already passed for this one --
			// scaling only slightly by distance keeps a clean match comfortably
			// above Consumer's DefaultAmbiguityThreshold rather than scaling it
			// down toward the same range as every other candidate.
			confidence = confidenceCeiling - candidate.Distance.Total*0.1
		}
		identifications = append(identifications, model.Identification{
			Strategy:   StrategyParse,
			ExternalID: &model.ExternalID{Provider: ProviderReleaseGroup, ExternalID: candidate.ID, Confidence: confidence},
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

// claimedIdentity is what the file itself says it is: real ARTIST/ALBUM/DATE tags
// when present (more reliable than a filename guess), the release-folder name
// through internal/identify/parse otherwise -- ADR-41's "path is evidence" for a file with
// no usable tags at all.
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

// cleanAlbumTag strips edition/technical noise a tagger baked directly into the
// ALBUM tag itself, not just the release folder name -- found against a real
// "Nogih Zeni (Mono Remastered)" tag during this project's own verification pass,
// which MusicBrainz's own exact-phrase search treats as a literally different,
// nonexistent title. Reuses internal/identify/parse's own bracket/noise classification
// (it handles a bare title exactly like a one-segment release name) rather than
// duplicating it; falls back to the raw tag if cleaning empties it out.
func cleanAlbumTag(album string) string {
	if cleaned := parse.Parse(album).Identity.Album; cleaned != "" {
		return cleaned
	}
	return album
}

// combinedTitle folds artist and album into one string. internal/identify/match's Identity
// has no dedicated artist dimension; comparing "Artist - Album" as a single title
// gives both album title and artist credit weight in the one dimension that
// exists, rather than needing a new one for what is otherwise a one-caller need.
func combinedTitle(artist, title string) string {
	if artist == "" {
		return title
	}
	return artist + " - " + title
}
