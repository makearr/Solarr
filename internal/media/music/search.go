package music

// Stage 5's own candidate pipeline (section 10.1): given one wanted album,
// search every configured indexer, match what comes back against the
// album's own identity, score it against the category's own quality/
// language profile, and persist ACCEPT/REJECT/DEFER. Dispatch
// (acquire.send, SENT/PREPARED/HELD) is a separate, later pass -- this is
// decide-and-persist only.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// SearchedOnItsOwn is music's answer to which desired items the planner hunts: albums, not tracks.
func SearchedOnItsOwn(satisfactionKind string) bool { return satisfactionKind == "SET" }

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
	ID int64
	// Name is what a person calls the indexer, for naming it when it fails or is slow.
	Name   string
	Impl   string // "newznab" | "torznab" (ADR-43) -- decides the stored protocol and which RawItem fields apply
	Client IndexerSearcher
	// MusicParams is what the indexer's caps say its music search accepts; nil when unknown.
	MusicParams []string
	// Categories scope every search and feed read to music (D17); nil asks without a category.
	Categories []int
}

// takesStructuredMusic reports whether an artist/album query can find anything here: an indexer
// whose caps list only "q" answers it with nothing (NZBgeek via Prowlarr, 2026-09-29).
func (c IndexerConn) takesStructuredMusic() bool {
	return c.MusicParams == nil || slices.Contains(c.MusicParams, "artist")
}

// IndexerSearchError records one indexer's own search failure -- best-effort
// across every configured indexer (ADR-37's own "partial results are
// normal, not an error"), never stopping the rest.
type IndexerSearchError struct {
	IndexerID   int64
	IndexerName string
	Error       string
	// TooSlow means the indexer had not answered when the group deadline passed (ADR-37).
	TooSlow bool
}

// indexerGroupDeadline bounds one tier's fan-out to every indexer (ADR-37's default), whatever
// each indexer's own timeout.
const indexerGroupDeadline = 20 * time.Second

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
	// Blocklist, when set, keeps a blocklisted release out before it is evaluated (section 10.11).
	Blocklist *library.BlocklistStore
	// IndexerDeadline bounds each tier's fan-out; zero is indexerGroupDeadline.
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
	if !SearchedOnItsOwn(item.SatisfactionKind) {
		return ItemSearchResult{}, ErrTrackSearchNotSupported
	}
	if err := s.Desired.RecordSearched(ctx, itemID, at.Unix()); err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}

	judge, err := s.judgeFor(ctx, item)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: search item %d: %w", itemID, err)
	}
	tiers := buildSearchTiers(judge.artistName, judge.work, judge.aliases)
	if custom != nil {
		tiers = [][]searchTerms{musicTerms(custom.Artist, custom.Album, "")}
		// The typed terms are the identity now; the work's year and ids would judge them against the old name.
		judge.albumTarget.Identity = match.Identity{Title: custom.Album, AlternateTitles: []string{judge.work.CanonicalTitle}}
		judge.target.Identity = match.Identity{
			Title: combinedTitle(custom.Artist, custom.Album), AlternateTitles: []string{judge.target.Title},
		}
		judge.wantedArtist = custom.Artist
	}

	result := ItemSearchResult{}
	for tierIndex, terms := range tiers {
		var accepted []acceptedFind
		answers, err := askEveryIndexer(ctx, indexers, terms, cmp.Or(s.IndexerDeadline, indexerGroupDeadline))
		if err != nil {
			return result, fmt.Errorf("music: search item %d: %w", itemID, err)
		}
		// Merged in indexer order, so a duplicate keeps the best-priority indexer's copy.
		for index, answer := range answers {
			conn := indexers[index]
			if answer.err != nil {
				result.IndexerErrors = append(result.IndexerErrors, IndexerSearchError{
					IndexerID: conn.ID, IndexerName: conn.Name, Error: answer.err.Error(), TooSlow: answer.tooSlow,
				})
			}
			raws, err := s.withoutBlocklisted(ctx, conn.ID, answer.raws, at)
			if err != nil {
				return result, fmt.Errorf("music: search item %d: %w", itemID, err)
			}
			accepted = append(accepted, judge.evaluate(raws, conn)...)
		}
		if len(accepted) == 0 {
			continue
		}
		tier := tierIndex
		result.TierUsed = &tier
		result.Decisions, err = s.decide(ctx, itemID, accepted, "search", at)
		return result, err
	}
	return result, nil
}

// judge is one album's yardstick for releases: who and what it is, and the library's quality rules.
type judge struct {
	work                librarydb.Work
	aliases             []string
	artistName          string
	wantedArtist        string
	target, albumTarget match.Candidate
	rules               []rankedProfile
	languagePolicy      *score.LanguagePolicy
	preferPropers       bool
}

// judgeFor builds item's yardstick from its work and its library's settings.
func (s Search) judgeFor(ctx context.Context, item librarydb.DesiredItem) (judge, error) {
	detail, err := s.Works.Detail(ctx, item.WorkID)
	if err != nil {
		return judge{}, fmt.Errorf("work detail: %w", err)
	}
	aliases, err := s.Works.AlternateTitles(ctx, item.WorkID)
	if err != nil {
		return judge{}, fmt.Errorf("alternate titles: %w", err)
	}
	snapshot := s.Settings.Snapshot()
	rules, err := libraryProfiles(snapshot, item.CategoryID, item.ProfileID)
	if err != nil {
		return judge{}, err
	}
	preferPropers := true
	if category, found := snapshot.Category(item.CategoryID); found {
		preferPropers = category.FileManagement.PropersRepacks != settings.PropersDoNotPrefer
	}
	artistName := primaryArtistName(detail)
	target := targetIdentity(detail, item, artistName, aliases)
	albumTarget := target
	albumTarget.Title = detail.Work.CanonicalTitle
	return judge{
		work: detail.Work, aliases: aliases, artistName: artistName, wantedArtist: artistName,
		target: target, albumTarget: albumTarget, rules: rules,
		languagePolicy: languagePolicyFor(snapshot, item.CategoryID), preferPropers: preferPropers,
	}, nil
}

func (j judge) evaluate(raws []indexer.RawItem, conn IndexerConn) []acceptedFind {
	return evaluateRawItems(raws, conn, j.target, j.albumTarget, j.wantedArtist, j.rules, j.languagePolicy, j.preferPropers)
}

// decide keeps one row per release, ranks them best first and records each decision against itemID.
func (s Search) decide(ctx context.Context, itemID int64, accepted []acceptedFind, source string, at time.Time) (
	[]library.DecisionDetail, error,
) {
	accepted = withoutCrossIndexerDuplicates(accepted)
	sortBestFirst(accepted)
	decisions := make([]library.DecisionDetail, 0, len(accepted))
	for _, found := range accepted {
		detail, err := s.persist(ctx, itemID, found, source, at)
		if err != nil {
			return decisions, fmt.Errorf("music: search item %d: persist: %w", itemID, err)
		}
		decisions = append(decisions, detail)
	}
	return decisions, nil
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
	if !SearchedOnItsOwn(item.SatisfactionKind) {
		return ItemSearchResult{}, ErrTrackSearchNotSupported
	}
	judge, err := s.judgeFor(ctx, item)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: judge feed for item %d: %w", itemID, err)
	}
	raws, err = s.withoutBlocklisted(ctx, conn.ID, raws, at)
	if err != nil {
		return ItemSearchResult{}, fmt.Errorf("music: judge feed for item %d: %w", itemID, err)
	}
	accepted := judge.evaluate(raws, conn)
	if len(accepted) == 0 {
		return ItemSearchResult{}, nil
	}
	decisions, err := s.decide(ctx, itemID, accepted, "rss", at)
	return ItemSearchResult{Decisions: decisions}, err
}

// indexerAnswer is what one indexer gave for one tier's queries, up to its first failure.
type indexerAnswer struct {
	raws    []indexer.RawItem
	err     error
	tooSlow bool
}

// askEveryIndexer runs a tier's queries against every indexer at once and waits for all of them,
// or the group deadline (ADR-37): one slow indexer no longer delays the others' results.
func askEveryIndexer(
	ctx context.Context, indexers []IndexerConn, terms []searchTerms, deadline time.Duration,
) ([]indexerAnswer, error) {
	group, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	answers := make([]indexerAnswer, len(indexers))
	var wait sync.WaitGroup
	for index, conn := range indexers {
		wait.Go(func() { answers[index] = askIndexer(group, conn, terms) })
	}
	wait.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for index := range answers {
		if answers[index].err != nil && errors.Is(group.Err(), context.DeadlineExceeded) &&
			errors.Is(answers[index].err, context.DeadlineExceeded) {
			answers[index].tooSlow = true
			answers[index].err = fmt.Errorf("no answer within %s; its releases are missing from this search", deadline)
		}
	}
	return answers, nil
}

// askIndexer sends one indexer a tier's queries in turn; a failing indexer is not asked again.
func askIndexer(ctx context.Context, conn IndexerConn, terms []searchTerms) indexerAnswer {
	var answer indexerAnswer
	seen := map[string]bool{}
	for _, term := range terms {
		if term.Extra != nil && !conn.takesStructuredMusic() {
			continue
		}
		raws, err := conn.Client.Search(ctx, indexer.SearchRequest{
			Function: "music", Query: term.Query, Extra: term.Extra, Categories: conn.Categories,
			Limit: candidateSearchResultLimit,
		})
		if err != nil {
			answer.err = err
			return answer
		}
		answer.raws = append(answer.raws, unseen(raws, seen)...)
	}
	return answer
}

// sortBestFirst ranks releases as a grab chooses them: accepted first, then the quality tiers
// (ADR-40), then the additive score, then the closer match.
func sortBestFirst(finds []acceptedFind) {
	slices.SortStableFunc(finds, func(a, b acceptedFind) int {
		if aOK, bOK := a.decision.Outcome == model.OutcomeAccept, b.decision.Outcome == model.OutcomeAccept; aOK != bOK {
			if aOK {
				return -1
			}
			return 1
		}
		if winner, _ := model.Compare(a.decision.Quality, b.decision.Quality); winner != 0 {
			return winner
		}
		if byScore := cmp.Compare(b.decision.Score(), a.decision.Score()); byScore != 0 {
			return byScore
		}
		return cmp.Compare(a.distance.Total, b.distance.Total)
	})
}

// duplicateSizeTolerance is how far two indexers' sizes for one release may differ (answered
// 2026-09-24: within 1%).
const duplicateSizeTolerance = 0.01

// withoutCrossIndexerDuplicates keeps one row for a release two indexers both list: same title
// once case and punctuation are ignored, size within 1%. Indexers are searched in priority order,
// so the first find, the best-priority indexer's, is kept.
func withoutCrossIndexerDuplicates(finds []acceptedFind) []acceptedFind {
	kept := finds[:0:0]
	for _, find := range finds {
		duplicate := slices.ContainsFunc(kept, func(earlier acceptedFind) bool {
			return earlier.conn.ID != find.conn.ID &&
				cleanSearchText(strings.ToLower(earlier.raw.Title)) == cleanSearchText(strings.ToLower(find.raw.Title)) &&
				sameSize(earlier.raw.SizeBytes, find.raw.SizeBytes)
		})
		if !duplicate {
			kept = append(kept, find)
		}
	}
	return kept
}

// sameSize is within duplicateSizeTolerance; an unknown size matches only another unknown one.
func sameSize(a, b int64) bool {
	if a <= 0 || b <= 0 {
		return a <= 0 && b <= 0
	}
	larger := float64(max(a, b))
	return (larger-float64(min(a, b)))/larger <= duplicateSizeTolerance
}

// unseen drops a release an earlier query of the same tier already returned from this indexer.
func unseen(raws []indexer.RawItem, seen map[string]bool) []indexer.RawItem {
	kept := raws[:0:0]
	for _, raw := range raws {
		if !seen[raw.GUID] {
			seen[raw.GUID] = true
			kept = append(kept, raw)
		}
	}
	return kept
}

// withoutBlocklisted drops a release blocklisted by guid on this indexer before it is evaluated
// (section 10.11), so it is neither decided nor offered again.
func (s Search) withoutBlocklisted(ctx context.Context, indexerID int64, raws []indexer.RawItem, at time.Time) ([]indexer.RawItem, error) {
	if s.Blocklist == nil {
		return raws, nil
	}
	kept := raws[:0:0]
	for _, raw := range raws {
		blocked, err := s.Blocklist.IsBlocked(ctx, &indexerID, raw.GUID, "", at.Unix())
		if err != nil {
			return nil, err
		}
		if !blocked {
			kept = append(kept, raw)
		}
	}
	return kept, nil
}

// BlocklistRelease blocklists the release a decision recorded against itemID, permanently and
// as the user's choice (section 10.11: only automatic entries expire).
func (s Search) BlocklistRelease(ctx context.Context, itemID, decisionID int64, at time.Time) error {
	if s.Blocklist == nil {
		return errors.New("music: blocklist is not available")
	}
	details, err := s.Candidates.ListDecisionsForItem(ctx, itemID, dispatchDecisionLookupLimit)
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
	raws []indexer.RawItem, conn IndexerConn, target, albumTarget match.Candidate, wantedArtist string,
	rules []rankedProfile, languagePolicy *score.LanguagePolicy, preferPropers bool,
) []acceptedFind {
	var accepted []acceptedFind
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
			}
		}
		distance := match.Compute(claimed, against, nil)
		if distance.Total > maxCandidateMatchDistance {
			continue
		}

		facts := factsFromRelease(parsed, raw, conn.Impl)
		decision := withRevision(evaluateInOrder(rules, facts, languagePolicy), facts.File.ProperRepack, preferPropers)
		accepted = append(accepted, acceptedFind{conn: conn, raw: raw, parsed: parsed, distance: distance, decision: decision})
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

// persist upserts the release and records the decision against itemID,
// exactly once per accepted find.
func (s Search) persist(ctx context.Context, itemID int64, found acceptedFind, source string, at time.Time) (library.DecisionDetail, error) {
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
		ParseConfidence: &confidence, Source: source,
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

// withRevision appends Sonarr's revision after the quality tiers when the library prefers propers:
// a proper or repack beats the same quality, never a better one, and before any additive score.
func withRevision(decision model.Decision, proper, prefer bool) model.Decision {
	if !prefer {
		return decision
	}
	tier := model.TierValue{Dimension: "revision", Rank: 1, Label: "original"}
	if proper {
		tier.Rank, tier.Label = 0, "proper or repack"
	}
	decision.Quality.Tiers = append(decision.Quality.Tiers, tier)
	return decision
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
