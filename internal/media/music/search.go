package music

// Stage 5's own candidate pipeline (section 10.1): given one wanted album,
// search every configured indexer, match what comes back against the
// album's own identity, score it against the category's own quality/
// language profile, and persist ACCEPT/REJECT/DEFER. Dispatch
// (acquire.send, SENT/PREPARED/HELD) is a separate, later pass -- this is
// decide-and-persist only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/identify/parse"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// ErrTrackSearchNotSupported reports a SINGLE (track-level) desired item --
// this pass only searches SET (album-level) items. A track search needs its
// own claimed identity (the track title, not the album's) and its own
// search terms; nothing has asked for that yet.
var ErrTrackSearchNotSupported = errors.New("music: search: only album-level (SET) desired items are supported")

// ErrNoQualityProfile reports a desired item whose own profile_id no longer
// resolves -- a deleted profile a category still points items at.
var ErrNoQualityProfile = errors.New("music: search: quality profile not found")

// maxCandidateMatchDistance is section 10.2's own acceptance gate for this
// pass -- reused verbatim from SearchIdentifier's own DefaultSearchThresholds
// (0.3), tuned there against real scene-release titles for the identical
// shape of comparison (combined "Artist - Album" text). A second threshold,
// AmbiguityMargin, does not apply here: SearchIdentifier disambiguates among
// several candidate *identities* for one file, this disambiguates among
// several *releases* against one already-known identity, so there is only
// ever one candidate identity to clear the gate against.
var maxCandidateMatchDistance = DefaultSearchThresholds.MaxDistance

// maxAliasSearchTerms bounds tier 2's own widening -- a work with many
// recorded alternate titles (rare today; nothing populates work_title yet,
// see Works.AlternateTitles) still only costs a handful of extra searches.
const maxAliasSearchTerms = 5

// candidateSearchResultLimit bounds one indexer's own per-tier response, the same
// ceiling SearchIdentifier already uses for a MusicBrainz search.
const candidateSearchResultLimit = 20

// defaultCandidateRetention is how long a found candidate stays queryable
// before DeleteExpiredCandidates would sweep it (section 10.1's own "cached
// until expires_at"). A placeholder until ADR-57's own retention.candidates
// setting exists (stage 5's Downloads section, not built yet) -- carried
// here so it needs no further change once that setting lands.
const defaultCandidateRetention = 14 * 24 * time.Hour

// IndexerSearcher is what the candidate pipeline needs from a configured
// indexer connection -- indexer.Client's own Search method, named
// separately from search_identifier.go's own Searcher (a MusicBrainz
// lookup, unrelated signature) sharing this package.
type IndexerSearcher interface {
	Search(ctx context.Context, request indexer.SearchRequest) ([]indexer.RawItem, error)
}

// IndexerConn is one configured, enabled indexer ready to search --
// building the client (credentials, egress) is the caller's own job
// (cmd/solarr/app/providers.go), the same split ArrSync's own
// LidarrClientFactory already keeps.
type IndexerConn struct {
	ID     int64
	Impl   string // "newznab" | "torznab" (ADR-43) -- decides the stored protocol and which RawItem fields apply
	Client IndexerSearcher
}

// IndexerSearchError records one indexer's own search failure -- best-effort
// across every configured indexer (ADR-37's own "partial results are
// normal, not an error"), never stopping the rest.
type IndexerSearchError struct {
	IndexerID int64
	Error     string
}

// ItemSearchResult is one desired item's own run: which tier (if any)
// produced an acceptable match, every decision persisted, and any indexer
// that failed along the way.
type ItemSearchResult struct {
	// TierUsed is nil when no tier found anything worth persisting -- not a
	// failure (section 10.1: a search finding nothing is not an error).
	TierUsed      *int
	Decisions     []library.DecisionDetail
	IndexerErrors []IndexerSearchError
}

// Search is stage 5's own candidate pipeline over one desired item at a
// time (todo: per-category iterates this, see SearchCategory).
type Search struct {
	Works      library.Works
	Desired    library.Desired
	Candidates library.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
}

// searchTerms is one query's own artist/album/year parameters -- Newznab's
// dedicated music-search function (ADR-43's caps already confirm real
// indexers implement it) takes these directly, sidestepping the still-open
// category-id mapping (private/questions.md) entirely for this pass.
type searchTerms struct {
	Extra map[string]string
}

// SearchItem runs the pipeline for one album-level desired item: build its
// own identity and search tiers, fan out to every indexer at each tier in
// turn, and stop widening at the first tier that finds at least one release
// clearing the match-distance gate (section 10.2's own progressive
// widening). Every accepted match is scored and persisted regardless of its
// quality outcome -- REJECT and DEFER are recorded findings, not silence.
func (s Search) SearchItem(ctx context.Context, itemID int64, indexers []IndexerConn, at time.Time) (ItemSearchResult, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	if item.SatisfactionKind != "SET" {
		return ItemSearchResult{}, ErrTrackSearchNotSupported
	}

	detail, err := s.Works.Detail(ctx, item.WorkID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: work detail: %w", itemID, err)
	}
	aliases, err := s.Works.AlternateTitles(ctx, item.WorkID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: alternate titles: %w", itemID, err)
	}

	snapshot := s.Settings.Snapshot()
	rules, err := libraryProfiles(snapshot, item.CategoryID, item.ProfileID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	languagePolicy := languagePolicyFor(snapshot, item.CategoryID)

	artistName := primaryArtistName(detail)
	target := targetIdentity(detail, item, artistName, aliases)
	tiers := buildSearchTiers(artistName, detail.Work, aliases)

	result := ItemSearchResult{}
	for tierIndex, terms := range tiers {
		var accepted []acceptedFind
		for _, conn := range indexers {
			for _, term := range terms {
				raws, searchErr := conn.Client.Search(ctx, indexer.SearchRequest{
					Function: "music-search", Extra: term.Extra, Limit: candidateSearchResultLimit,
				})
				if searchErr != nil {
					result.IndexerErrors = append(result.IndexerErrors,
						IndexerSearchError{IndexerID: conn.ID, Error: searchErr.Error()})
					continue
				}
				accepted = append(accepted, evaluateRawItems(raws, conn, target, rules, languagePolicy)...)
			}
		}
		if len(accepted) == 0 {
			continue
		}
		tier := tierIndex
		result.TierUsed = &tier
		for _, found := range accepted {
			decisionDetail, persistErr := s.persist(ctx, itemID, found, at)
			if persistErr != nil {
				return result, fmt.Errorf("music: search item %d: persist: %w", itemID, persistErr)
			}
			result.Decisions = append(result.Decisions, decisionDetail)
		}
		return result, nil
	}
	return result, nil
}

// acceptedFind is one raw item that cleared the match-distance gate against
// the target identity, carrying everything persist needs.
type acceptedFind struct {
	conn     IndexerConn
	raw      indexer.RawItem
	parsed   parse.Result
	distance match.Distance
	decision model.Decision
}

// evaluateRawItems parses, matches and scores every raw result from one
// indexer call, keeping only what clears maxCandidateMatchDistance -- a
// release that plainly is not this album is noise, not evidence, and is
// never persisted (section 10.1: module.Evaluate produces Matches, not raw
// items).
func evaluateRawItems(
	raws []indexer.RawItem, conn IndexerConn, target match.Candidate,
	rules []rankedProfile, languagePolicy *score.LanguagePolicy,
) []acceptedFind {
	var accepted []acceptedFind
	for _, raw := range raws {
		parsed := parse.Parse(raw.Title)
		claimed := match.Claimed{Identity: match.Identity{
			Title: combinedTitle(parsed.Identity.Artist, parsed.Identity.Album),
			Year:  parsed.Identity.Year,
		}}
		distance := match.Compute(claimed, target, nil)
		if distance.Total > maxCandidateMatchDistance {
			continue
		}

		facts := factsFromRelease(parsed, raw, conn.Impl)
		decision := evaluateInOrder(rules, facts, languagePolicy)
		accepted = append(accepted, acceptedFind{conn: conn, raw: raw, parsed: parsed, distance: distance, decision: decision})
	}
	return accepted
}

// persist upserts the release and records the decision against itemID,
// exactly once per accepted find.
func (s Search) persist(ctx context.Context, itemID int64, found acceptedFind, at time.Time) (library.DecisionDetail, error) {
	var sizeBytes *int64
	if found.raw.SizeBytes > 0 {
		size := found.raw.SizeBytes
		sizeBytes = &size
	}
	var seeders, leechers *int64
	if found.conn.Impl == "torznab" {
		s, l := int64(found.raw.Seeders), int64(found.raw.Peers)
		seeders, leechers = &s, &l
	}
	var publishedAt *int64
	if !found.raw.PublishedAt.IsZero() {
		p := found.raw.PublishedAt.Unix()
		publishedAt = &p
	}
	confidence := found.parsed.Confidence

	candidate, err := s.Candidates.Upsert(ctx, library.NewReleaseCandidate{
		IndexerID: found.conn.ID, GUID: found.raw.GUID, Title: found.raw.Title,
		SizeBytes: sizeBytes, Seeders: seeders, Leechers: leechers, PublishedAt: publishedAt,
		Protocol: protocolForImpl(found.conn.Impl), DownloadURL: found.raw.Link,
		ParseConfidence: &confidence, Source: "search",
		FirstSeenAt: at.Unix(), ExpiresAt: at.Add(defaultCandidateRetention).Unix(),
	})
	if err != nil {
		return library.DecisionDetail{}, err
	}

	matchConfidence := 1 - found.distance.Total
	decision, err := s.Candidates.RecordDecision(ctx, library.NewDecision{
		CandidateID: candidate.ID, DesiredItemID: itemID, Outcome: string(found.decision.Outcome),
		Score:           found.decision.Score(),
		ComponentsJSON:  jsonOrEmptyArray(found.decision.Components),
		RejectionsJSON:  jsonOrEmptyArray(found.decision.Rejections),
		DeferralsJSON:   jsonOrEmptyArray(found.decision.Deferrals),
		MatchConfidence: &matchConfidence, DecidedAt: at.Unix(),
	})
	if err != nil {
		return library.DecisionDetail{}, err
	}
	return library.DecisionDetail{Decision: decision, ReleaseCandidate: candidate}, nil
}

// jsonOrEmptyArray marshals v, answering a literal "[]" for a nil slice --
// components_json is NOT NULL, and a bare Marshal(nil) answers "null",
// which is a valid string but not a valid array for a reader expecting one.
func jsonOrEmptyArray(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil || string(encoded) == "null" {
		return "[]"
	}
	return string(encoded)
}

// targetIdentity builds the album's own claimed-against identity once per
// search. ItemCount is the album's own required track count (SET only,
// already guaranteed above); ExternalIDs are carried through even though no
// raw search result will ever have one to compare against yet -- tier 4's
// own metadata-assisted search (section 10.2, not built this pass) is where
// that stops being true.
func targetIdentity(detail library.WorkDetail, item librarydb.DesiredItem, artistName string, aliases []string) match.Candidate {
	identity := match.Identity{
		Title:           combinedTitle(artistName, detail.Work.CanonicalTitle),
		AlternateTitles: aliases,
	}
	if detail.Work.Year.Valid {
		identity.Year = int(detail.Work.Year.Int64)
	}
	if item.RequiredCount.Valid {
		identity.ItemCount = int(item.RequiredCount.Int64)
	}
	for _, external := range detail.ExternalIDs {
		identity.ExternalIDs = append(identity.ExternalIDs, match.ExternalID{
			Provider: external.Provider, ID: external.ExternalID,
		})
	}
	return match.Candidate{ID: strconv.FormatInt(item.WorkID, 10), Identity: identity}
}

// primaryArtistName reads the work's own artist=ord0 credit (AddCredit's
// shape, ArrSync's syncAlbum among its callers) -- falling back to the
// denormalised sort name when no credit exists, for a work that predates
// crediting or was never given one.
func primaryArtistName(detail library.WorkDetail) string {
	for _, credit := range detail.Credits {
		if credit.Role == "artist" {
			return credit.PersonName
		}
	}
	return detail.Work.PrimaryArtistSortName.String
}

// buildSearchTiers is section 10.2's own progressive widening, scoped to
// tiers 0-2 this pass (exact, +year, +alias -- tiers 3/4, fuzzy composite
// and metadata-assisted id search, are open). Tier 1 is omitted entirely
// when the work has no recorded year, rather than repeating tier 0's own
// query for no reason.
func buildSearchTiers(artistName string, work librarydb.Work, aliases []string) [][]searchTerms {
	tiers := [][]searchTerms{{{Extra: map[string]string{"artist": artistName, "album": work.CanonicalTitle}}}}

	if work.Year.Valid {
		tiers = append(tiers, []searchTerms{{Extra: map[string]string{
			"artist": artistName, "album": work.CanonicalTitle,
			"year": strconv.FormatInt(work.Year.Int64, 10),
		}}})
	}

	if len(aliases) > 0 {
		limited := aliases
		if len(limited) > maxAliasSearchTerms {
			limited = limited[:maxAliasSearchTerms]
		}
		terms := make([]searchTerms, len(limited))
		for i, alias := range limited {
			terms[i] = searchTerms{Extra: map[string]string{"artist": artistName, "album": alias}}
		}
		tiers = append(tiers, terms)
	}

	return tiers
}

// languagePolicyFor resolves the category's own language profile, nil when
// none is assigned -- language stays an optional axis (section 19.8).
func languagePolicyFor(snapshot *settings.Snapshot, categoryID int64) *score.LanguagePolicy {
	category, found := snapshot.Category(categoryID)
	if !found || category.LanguageProfileID == nil {
		return nil
	}
	profile, found := snapshot.LanguageProfile(*category.LanguageProfileID)
	if !found {
		return nil
	}
	return &score.LanguagePolicy{
		AudioRequired: profile.AudioRequired, AudioPreferred: profile.AudioPreferred,
		SubtitleRequired: profile.SubtitleRequired, SubtitlePreferred: profile.SubtitlePreferred,
		PreferOriginalAudio: profile.PreferOriginalAudio, RejectDubbed: profile.RejectDubbed,
	}
}

// protocolForImpl maps an indexer's own protocol family to candidate.protocol's
// vocabulary (ADR-43: Newznab is usenet, Torznab is torrent -- Solarr
// speaks no other indexer protocol yet).
func protocolForImpl(impl string) string {
	if impl == "torznab" {
		return "torrent"
	}
	return "usenet"
}

// factsFromRelease stands a claimed release's own parse in for measured
// facts (ADR-39) -- the same reasoning apiserver.SimulateScore's own
// factsFromParsed already uses, extended with the release's own reported
// size/seeders/protocol (score.ReleaseFacts, known before any download).
func factsFromRelease(parsed parse.Result, raw indexer.RawItem, impl string) score.Facts {
	facts := score.Facts{
		Release: score.ReleaseFacts{
			Title: raw.Title, ReleaseGroup: parsed.Attributes.ReleaseGroup,
			Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: protocolForImpl(impl),
		},
		File: score.FileFacts{
			AudioCodec: parsed.Attributes.Format, Lossless: parsed.Attributes.Lossless,
			Source: parsed.Attributes.Source, ReleaseGroup: parsed.Attributes.ReleaseGroup,
		},
	}
	if parsed.Attributes.BitrateKbps > 0 {
		facts.File.OverallBitrate = int64(parsed.Attributes.BitrateKbps) * 1000
	}
	if parsed.Attributes.Edition != "" {
		facts.File.EditionTokens = []string{parsed.Attributes.Edition}
	}
	return facts
}

// rankedProfile is one of a library's profiles, in its priority order.
type rankedProfile struct {
	name  string
	rules score.Profile
}

// libraryProfiles is the library's ordered list (2026-09-24), or the item's own profile when the
// library has none.
func libraryProfiles(snapshot *settings.Snapshot, categoryID, itemProfileID int64) ([]rankedProfile, error) {
	ids := []int64{itemProfileID}
	if category, found := snapshot.Category(categoryID); found && len(category.QualityProfileIDs) > 0 {
		ids = category.QualityProfileIDs
	}
	profiles := make([]rankedProfile, 0, len(ids))
	for _, id := range ids {
		profile, found := snapshot.QualityProfile(id)
		if !found {
			return nil, ErrNoQualityProfile
		}
		rules, err := score.Parse([]byte(profile.RulesJSON))
		if err != nil {
			return nil, fmt.Errorf("quality profile %d: %w", id, err)
		}
		profiles = append(profiles, rankedProfile{name: profile.Name, rules: rules})
	}
	return profiles, nil
}

// evaluateInOrder judges a release by the first profile it fits, so a release the first profile
// wants outranks one only a fallback wants; failing that, by the first whose rules merely accept
// it, ranked after every fit. A release no profile accepts is judged, and refused, by the first.
// The profile's position is the leading tier of the quality vector (ADR-40: tiers, not weights).
func evaluateInOrder(profiles []rankedProfile, facts score.Facts, languagePolicy *score.LanguagePolicy) model.Decision {
	evaluate := func(rules score.Profile) model.Decision {
		if languagePolicy != nil {
			return score.EvaluateWithLanguage(rules, facts, nil, *languagePolicy,
				facts.File.AudioLanguages, facts.File.SubtitleLanguages)
		}
		return score.Evaluate(rules, facts, nil)
	}
	fallback, fallbackIndex := model.Decision{}, -1
	for index, profile := range profiles {
		decision := evaluate(profile.rules)
		if decision.Outcome != model.OutcomeAccept {
			continue
		}
		if fits, _ := score.Fits(profile.rules, facts); fits {
			return rankedBy(decision, index, profile.name, "fits")
		}
		if fallbackIndex < 0 {
			fallback, fallbackIndex = decision, index
		}
	}
	if fallbackIndex >= 0 {
		return rankedBy(fallback, len(profiles)+fallbackIndex, profiles[fallbackIndex].name, "accepted, fits none")
	}
	return evaluate(profiles[0].rules)
}

func rankedBy(decision model.Decision, rank int, name, how string) model.Decision {
	tier := model.TierValue{
		Dimension: "profile_priority", Rank: rank, Label: name, Detail: fmt.Sprintf("%s (%s)", name, how),
	}
	decision.Quality.Tiers = append([]model.TierValue{tier}, decision.Quality.Tiers...)
	// Zero points: the stored breakdown says which profile wanted it without changing the score.
	decision.Components = append(decision.Components, model.ScoreComponent{
		Rule: "profile-priority", Label: fmt.Sprintf("Wanted by %s (%s)", name, how),
	})
	return decision
}
