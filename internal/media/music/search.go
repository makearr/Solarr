package music

// Stage 5's own candidate pipeline (section 10.1): given one wanted album,
// search every configured indexer, match what comes back against the
// album's own identity, score it against the category's own quality/
// language profile, and persist ACCEPT/REJECT/DEFER. Dispatch
// (acquire.send, SENT/PREPARED/HELD) is a separate, later pass -- this is
// decide-and-persist only.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/statistics"
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

// ErrNoQualityProfile reports a desired item whose profile no longer exists; the shared error, under music's name.
var ErrNoQualityProfile = judge.ErrNoQualityProfile

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

// candidateSearchResultLimit bounds one indexer's answer to one query, the ceiling SearchIdentifier uses too.
const candidateSearchResultLimit = 20

// IndexerSearcher is an indexer's search as the pipeline needs it: the shared type under music's older name.
type IndexerSearcher = judge.Searcher

// IndexerConn is one configured, enabled indexer ready to search: the shared type under music's older name.
type IndexerConn = judge.Conn

// musicFunction is the Newznab function music searches with; an indexer's caps for it sit under this key.
const musicFunction = "music"

// IndexerSearchError is one indexer's failure within a search; the shared type under music's older name.
type IndexerSearchError = judge.IndexerError

// ItemSearchResult is one wanted album's search; the shared result under music's older name.
type ItemSearchResult = judge.Result

// Search is stage 5's own candidate pipeline over one desired item at a
// time (todo: per-category iterates this, see SearchCategory).
type Search struct {
	Works      library.Works
	Desired    library.Desired
	Candidates statistics.Candidates
	Settings   *settings.Cache
	Clock      clock.Clock
	// Blocklist, when set, keeps a blocklisted release out before it is evaluated (section 10.11).
	Blocklist *library.BlocklistStore
	// Sightings, when set, remember what RSS showed for "fresh" (ADR-55).
	Sightings *library.Sightings
	// IndexerDeadline bounds each tier's fan-out; zero is judge.GroupDeadline.
	IndexerDeadline time.Duration
}

// SearchTerms are a person's own artist and album for "search with different terms".
type SearchTerms struct {
	Artist string
	Album  string
}

// searchTerms is one t=music query: structured artist/album/year in Extra, or free text in Query.
// Both are sent, as Lidarr does: NZBgeek through Prowlarr answers only the text form (2026-09-25).
type searchTerms struct {
	Query string
	Extra map[string]string
}

// musicTerms is one album's structured query and its text query in every spelling
// searchTextVariants gives; an unknown artist drops the structured one.
func musicTerms(artist, album, year string) []searchTerms {
	var terms []searchTerms
	if artist != "" {
		extra := map[string]string{"artist": artist, "album": album}
		if year != "" {
			extra["year"] = year
		}
		terms = append(terms, searchTerms{Extra: extra})
	}
	words := []string{}
	for _, word := range []string{artist, album, year} {
		if word != "" {
			words = append(words, word)
		}
	}
	for _, text := range searchTextVariants(strings.Join(words, " ")) {
		terms = append(terms, searchTerms{Query: text})
	}
	return terms
}

// queriesFor turns a tier's terms into indexer queries: a structured one is only worth sending to an indexer whose
// caps list the artist parameter (NZBgeek through Prowlarr answers the text form only, 2026-09-29).
func queriesFor(terms []searchTerms) []judge.Query {
	queries := make([]judge.Query, 0, len(terms))
	for _, term := range terms {
		query := judge.Query{Function: musicFunction, Text: term.Query, Extra: term.Extra, Limit: candidateSearchResultLimit}
		if term.Extra != nil {
			query.Needs = []string{"artist"}
		}
		queries = append(queries, query)
	}
	return queries
}

// SearchItem runs the pipeline for one album-level desired item: build its
// own identity and search tiers, fan out to every indexer at each tier in
// turn, and stop widening at the first tier that finds at least one release
// clearing the match-distance gate (section 10.2's own progressive
// widening). Every accepted match is scored and persisted regardless of its
// quality outcome -- REJECT and DEFER are recorded findings, not silence.
func (s Search) SearchItem(ctx context.Context, itemID int64, indexers []IndexerConn, at time.Time) (ItemSearchResult, error) {
	return s.SearchItemWithTerms(ctx, itemID, indexers, at, nil)
}

// SearchItemWithTerms is SearchItem searching a person's own terms instead of the work's tiers; a
// release matching them is accepted as matching the work.
func (s Search) SearchItemWithTerms(
	ctx context.Context, itemID int64, indexers []IndexerConn, at time.Time, custom *SearchTerms,
) (ItemSearchResult, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	if !plan.SearchedOnItsOwn(item.SatisfactionKind) {
		return ItemSearchResult{}, ErrTrackSearchNotSupported
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}

	measure, err := s.yardstickFor(ctx, item)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	tiers := buildSearchTiers(measure.artistName, measure.work, measure.aliases)
	if custom != nil {
		tiers = [][]searchTerms{musicTerms(custom.Artist, custom.Album, "")}
		// The typed terms are the identity now; the work's year and ids would judge them against the old name.
		measure.albumTarget.Identity = match.Identity{Title: custom.Album, AlternateTitles: []string{measure.work.CanonicalTitle}}
		measure.target.Identity = match.Identity{
			Title: combinedTitle(custom.Artist, custom.Album), AlternateTitles: []string{measure.target.Title},
		}
		measure.wantedArtist = custom.Artist
	}

	queries := make([][]judge.Query, len(tiers))
	for index, terms := range tiers {
		queries[index] = queriesFor(terms)
	}
	result, err := s.pipeline().Search(ctx, itemID, indexers, queries, measure.evaluate, at)
	if err != nil {
		return result, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	return result, nil
}

// pipeline is the shared search over this type's stores.
func (s Search) pipeline() judge.Pipeline {
	return judge.Pipeline{Candidates: s.Candidates, Desired: s.Desired, Sightings: s.Sightings, Blocklist: s.Blocklist, Deadline: s.IndexerDeadline}
}

// yardstick is what one album's releases are measured against: who and what it is, and the library's quality rules.
type yardstick struct {
	work                librarydb.Work
	aliases             []string
	artistName          string
	wantedArtist        string
	target, albumTarget match.Candidate
	rules               []judge.RankedProfile
	languagePolicy      *score.LanguagePolicy
	preferPropers       bool
	// incumbent is the worst owned file's decision when the album is below its cutoff: a release must
	// strictly beat it to be an upgrade (Invariant 72). Nil otherwise.
	incumbent *model.Decision
	// cutoffMet rejects every release, as Lidarr's "existing files meet cutoff": the album wants nothing.
	cutoffMet bool
	// delay is the library's delay profile: the other protocol's releases wait (D16); zero is none.
	delay judge.ProtocolDelay
}

// yardstickFor builds item's yardstick from its work and its library's settings.
func (s Search) yardstickFor(ctx context.Context, item librarydb.DesiredItem) (yardstick, error) {
	detail, err := s.Works.Detail(ctx, item.WorkID)
	if err != nil {
		return yardstick{}, fmt.Errorf("work detail: %w", err)
	}
	aliases, err := s.Works.AlternateTitles(ctx, item.WorkID)
	if err != nil {
		return yardstick{}, fmt.Errorf("alternate titles: %w", err)
	}
	snapshot := s.Settings.Snapshot()
	rules, err := judge.LibraryProfiles(ctx, s.Desired, snapshot, item.ID, item.CategoryID, item.ProfileID)
	if err != nil {
		return yardstick{}, err
	}
	preferPropers := true
	if category, found := snapshot.Category(item.CategoryID); found {
		preferPropers = category.FileManagement.PropersRepacks != settings.PropersDoNotPrefer
	}
	artistName := primaryArtistName(detail)
	var delay judge.ProtocolDelay
	if category, found := snapshot.Category(item.CategoryID); found {
		delay = judge.ProtocolDelay{
			Preferred: category.PreferredProtocol, Window: time.Duration(category.ProtocolDelayMinutes) * time.Minute, Now: s.Clock.Now(),
		}
	}
	target := targetIdentity(detail, item, artistName, aliases)
	albumTarget := target
	albumTarget.Title = detail.Work.CanonicalTitle
	built := yardstick{
		work: detail.Work, aliases: aliases, artistName: artistName, wantedArtist: artistName,
		target: target, albumTarget: albumTarget, rules: rules,
		languagePolicy: judge.LanguagePolicyFor(snapshot, item.CategoryID), preferPropers: preferPropers, delay: delay,
	}
	built.cutoffMet = item.State == ItemStateComplete
	if item.State == ItemStateUpgradable {
		if built.incumbent, err = s.incumbentFor(ctx, item.WorkID, built); err != nil {
			return yardstick{}, err
		}
	}
	return built, nil
}

// incumbentFor is the worst of the album's measured files, judged by the library's own profiles.
func (s Search) incumbentFor(ctx context.Context, workID int64, j yardstick) (*model.Decision, error) {
	files, err := s.Desired.MeasuredFiles(ctx, workID)
	if err != nil {
		return nil, err
	}
	var worst *model.Decision
	for _, file := range files {
		if !file.Measured {
			continue
		}
		decision := judge.EvaluateInOrder(j.rules, measuredFacts(file), j.languagePolicy)
		if worst == nil || judge.StrictlyBetter(*worst, decision) {
			worst = &decision
		}
	}
	return worst, nil
}

func (j yardstick) evaluate(raws []indexer.RawItem, conn IndexerConn) []judge.Find {
	finds := evaluateRawItems(raws, conn, j.target, j.albumTarget, j.wantedArtist, j.rules, j.languagePolicy, j.preferPropers)
	j.delay.Apply(finds)
	if j.cutoffMet {
		for index := range finds {
			decision := &finds[index].Decision
			if decision.Outcome == model.OutcomeAccept {
				decision.Outcome = model.OutcomeReject
				decision.Rejections = append(decision.Rejections, model.Rejection{
					Rule: "upgrade", Field: "quality", Wanted: "files below the cutoff", Got: "the files you have meet it",
				})
			}
		}
		return finds
	}
	if j.incumbent == nil {
		return finds
	}
	for index := range finds {
		decision := &finds[index].Decision
		if decision.Outcome == model.OutcomeAccept && !judge.StrictlyBetter(*decision, *j.incumbent) {
			decision.Outcome = model.OutcomeReject
			decision.Rejections = append(decision.Rejections, model.Rejection{
				Rule: "upgrade", Field: "quality", Wanted: "better than the files you have", Got: "not better",
			})
		}
	}
	return finds
}

// JudgeFeed judges releases an RSS sync found against one wanted album, exactly as a search's
// would be; nothing is searched, and the album's search record is left alone.
func (s Search) JudgeFeed(
	ctx context.Context, itemID int64, conn IndexerConn, raws []indexer.RawItem, at time.Time,
) (ItemSearchResult, error) {
	item, err := s.Desired.Get(ctx, itemID)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: judge feed for item %d: %w", itemID, err)
	}
	if !plan.SearchedOnItsOwn(item.SatisfactionKind) {
		return ItemSearchResult{}, ErrTrackSearchNotSupported
	}
	measure, err := s.yardstickFor(ctx, item)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: judge feed for item %d: %w", itemID, err)
	}
	result, err := s.pipeline().Feed(ctx, itemID, conn, raws, measure.evaluate, at)
	if err != nil {
		return result, fmt.Errorf("music: judge feed for item %d: %w", itemID, err)
	}
	return result, nil
}

// BlocklistRelease blocklists the release a decision recorded against itemID, permanently and
// as the user's choice (section 10.11: only automatic entries expire).
func (s Search) BlocklistRelease(ctx context.Context, itemID, decisionID int64, at time.Time) error {
	if s.Blocklist == nil {
		return errors.New("music: blocklist is not available")
	}
	details, err := s.Candidates.ListDecisionsForItem(ctx, itemID, dispatch.DecisionLookupLimit)
	if err != nil {
		return fmt.Errorf("music: blocklist release: %w", err)
	}
	for _, detail := range details {
		if detail.Decision.ID != decisionID {
			continue
		}
		indexerID := detail.ReleaseCandidate.IndexerID
		_, err := s.Blocklist.Create(ctx, library.NewBlocklistEntry{
			IndexerID: &indexerID, GUID: detail.ReleaseCandidate.GUID, Title: detail.ReleaseCandidate.Title,
			DesiredItemID: &itemID, Reason: library.BlocklistManual, Source: library.BlocklistSourceUser,
			CreatedAt: at.Unix(),
		})
		return err
	}
	return ErrDecisionNotFound
}

// evaluateRawItems parses, matches and scores every raw result from one
// indexer call, keeping only what clears maxCandidateMatchDistance -- a
// release that plainly is not this album is noise, not evidence, and is
// never persisted (section 10.1: module.Evaluate produces Matches, not raw
// items).
func evaluateRawItems(
	raws []indexer.RawItem, conn IndexerConn, target, albumTarget match.Candidate, wantedArtist string,
	rules []judge.RankedProfile, languagePolicy *score.LanguagePolicy, preferPropers bool,
) []judge.Find {
	var accepted []judge.Find
	for _, raw := range raws {
		parsed := parse.Parse(raw.Title)
		if !sameArtist(parsed.Identity.Artist, wantedArtist) || videoRelease(raw.Title) {
			continue
		}
		// The artist is gated above; comparing "artist - album" whole let the shared artist carry
		// another album of theirs through (Third for Portishead, 2026-09-29).
		claimed := match.Claimed{Identity: match.Identity{Title: parsed.Identity.Album, Year: parsed.Identity.Year}}
		against := albumTarget
		if parsed.Identity.Artist == "" || parsed.Identity.Album == "" {
			claimed.Title, against = combinedTitle(parsed.Identity.Artist, parsed.Identity.Album), target
			if rest, found := withoutLeadingArtist(parsed.Identity.Album, wantedArtist); found {
				claimed.Title, against = rest, albumTarget
			} else if parsed.Identity.Album == "" {
				// No album parsed: the artist alone would match every album of theirs, so judge what follows it.
				rest, found := withoutLeadingArtist(raw.Title, wantedArtist)
				if !found || rest == "" {
					continue
				}
				claimed.Title, against = rest, albumTarget
			}
		}
		distance := match.Compute(claimed, against, nil)
		if distance.Total > maxCandidateMatchDistance || albumTitleSimilarity(claimed.Title, against) < minReleaseTitleSimilarity {
			continue
		}
		// A single or EP named like the wanted album plus words ("I Am Happy" for "I Am") is another record.
		if smallFormat(raw.Title) && match.ExtraDistinctiveWords(albumTarget.Title, claimed.Title) {
			continue
		}

		facts := factsFromRelease(parsed, raw, conn.Impl)
		decision := judge.WithRevision(judge.EvaluateInOrder(rules, facts, languagePolicy), facts.File.ProperRepack, preferPropers)
		accepted = append(accepted, judge.Find{
			Conn: conn, Raw: raw, ParseConfidence: parsed.Confidence, Distance: distance.Total, Decision: decision,
		})
	}
	return accepted
}

// withoutLeadingArtist cuts the wanted artist off a name the parser could not split
// ("Kavelori2005Remixes"), so the rest is judged as the album alone.
func withoutLeadingArtist(name, artist string) (string, bool) {
	if artist == "" || len(name) <= len(artist) || !strings.EqualFold(name[:len(artist)], artist) {
		return "", false
	}
	return strings.TrimLeft(name[len(artist):], " .-_"), true
}

// videoMarkerRe matches scene tokens only a video release carries; a concert DVD rip was accepted
// for an album by the first automatic search (2026-09-29).
var videoMarkerRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:dvd-?rip|dvd[r59]|bd-?rip|br-?rip|blu-?ray|web-?dl|web-?rip|hdtv|hdrip|x26[45]|h\.?26[45]|xvid|divx|hevc|(?:480|576|720|1080|2160)[pi])(?:$|[^a-z0-9])`)

// minReleaseTitleSimilarity drops a release whose album title is another record's: a matching year alone carried
// "Discography" and a single's title under the distance cut-off for a self-titled album (2026-10-21).
const minReleaseTitleSimilarity = 0.75

// albumTitleSimilarity is how alike a claimed title is to the album's title or any of its alternate titles.
func albumTitleSimilarity(claimed string, album match.Candidate) float64 {
	best := match.TitleSimilarity(claimed, album.Title)
	for _, alternate := range album.AlternateTitles {
		best = max(best, match.TitleSimilarity(claimed, alternate))
	}
	return best
}

// smallFormatRe matches the scene tokens of a single or EP.
var smallFormatRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:single|ep|maxi)(?:$|[^a-z0-9])`)

func smallFormat(title string) bool { return smallFormatRe.MatchString(title) }

// videoRelease reports a release name that is a video, not an album.
func videoRelease(title string) bool { return videoMarkerRe.MatchString(title) }

// minArtistSimilarity is how alike a release's artist must be to the album's; the combined
// "artist album" distance alone let another artist's same-named single through (2026-09-29).
const minArtistSimilarity = 0.5

// sameArtist: a release naming no artist, or an album without one, passes; so does a
// compilation, and an artist named inside the other ("feat.", "&").
func sameArtist(released, wanted string) bool {
	if released == "" || wanted == "" || strings.EqualFold(strings.TrimSpace(wanted), "Various Artists") {
		return true
	}
	a, b := strings.ToLower(released), strings.ToLower(wanted)
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return true
	}
	return match.TitleSimilarity(released, wanted) >= minArtistSimilarity
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
	return detail.Work.LeadPersonSortName.String
}

// buildSearchTiers is section 10.2's own progressive widening, scoped to
// tiers 0-2 this pass (exact, +year, +alias -- tiers 3/4, fuzzy composite
// and metadata-assisted id search, are open). Tier 1 is omitted entirely
// when the work has no recorded year, rather than repeating tier 0's own
// query for no reason.
func buildSearchTiers(artistName string, work librarydb.Work, aliases []string) [][]searchTerms {
	tiers := [][]searchTerms{musicTerms(artistName, work.CanonicalTitle, "")}

	if work.Year.Valid {
		tiers = append(tiers, musicTerms(artistName, work.CanonicalTitle, strconv.FormatInt(work.Year.Int64, 10)))
	}

	if len(aliases) > 0 {
		limited := aliases
		if len(limited) > maxAliasSearchTerms {
			limited = limited[:maxAliasSearchTerms]
		}
		var terms []searchTerms
		for _, alias := range limited {
			terms = append(terms, musicTerms(artistName, alias, "")...)
		}
		tiers = append(tiers, terms)
	}

	return tiers
}

// factsFromRelease stands a claimed release's own parse in for measured
// facts (ADR-39) -- the same reasoning apiserver.SimulateScore's own
// factsFromParsed already uses, extended with the release's own reported
// size/seeders/protocol (score.ReleaseFacts, known before any download).
func factsFromRelease(parsed parse.Result, raw indexer.RawItem, impl string) score.Facts {
	facts := score.Facts{
		Release: score.ReleaseFacts{
			Title: raw.Title, ReleaseGroup: parsed.Attributes.ReleaseGroup,
			Seeders: raw.Seeders, Leechers: raw.Peers, Protocol: judge.ProtocolFor(impl),
		},
		File: score.FileFacts{
			AudioCodec: parsed.Attributes.Format, Lossless: parsed.Attributes.Lossless,
			Source: parsed.Attributes.Source, ReleaseGroup: parsed.Attributes.ReleaseGroup,
			ProperRepack: parsed.Attributes.ProperRepack,
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
