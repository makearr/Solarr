package judge

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/identify/model"
)

// RetentionDefault is how long a found candidate stays queryable before the sweep may remove it.
const RetentionDefault = 14 * 24 * time.Hour

// Find is one release that cleared a media type's match gate, with its decision.
type Find struct {
	Conn Conn
	Raw  indexer.RawItem
	// ParseConfidence is how much of the name the parser understood.
	ParseConfidence float64
	// Distance is how far the release is from what was wanted, 0 exact; it breaks ties between equal decisions.
	Distance float64
	// MatchConfidence is stored on the decision; zero takes 1 - Distance.
	MatchConfidence float64
	Decision        model.Decision
}

// SortBestFirst ranks releases as a grab chooses them: accepted first, then the quality tiers (ADR-40), then the
// additive score, then the closer match, then the preferred indexer (Sonarr's indexer priority, user 2026-10-23).
func SortBestFirst(finds []Find) {
	slices.SortStableFunc(finds, func(a, b Find) int {
		if aOK, bOK := a.Decision.Outcome == model.OutcomeAccept, b.Decision.Outcome == model.OutcomeAccept; aOK != bOK {
			if aOK {
				return -1
			}
			return 1
		}
		if winner, _ := model.Compare(a.Decision.Quality, b.Decision.Quality); winner != 0 {
			return winner
		}
		if byScore := cmp.Compare(b.Decision.Score(), a.Decision.Score()); byScore != 0 {
			return byScore
		}
		if byDistance := cmp.Compare(a.Distance, b.Distance); byDistance != 0 {
			return byDistance
		}
		return cmp.Compare(a.Conn.Priority, b.Conn.Priority)
	})
}

// duplicateSizeTolerance is how far two indexers' sizes for one release may differ (within 1%).
const duplicateSizeTolerance = 0.01

// WithoutCrossIndexerDuplicates keeps one row for a release listed twice, by two indexers or twice by one: same title once case and
// punctuation are ignored, size within 1%. Indexers are searched in priority order, so the first find is kept.
func WithoutCrossIndexerDuplicates(finds []Find) []Find {
	kept := finds[:0:0]
	keptTitles := make([]string, 0, len(finds))
	for _, find := range finds {
		title := cleanTitle(find.Raw.Title)
		duplicate := false
		for position, earlier := range kept {
			if keptTitles[position] == title && sameSize(earlier.Raw.SizeBytes, find.Raw.SizeBytes) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			kept = append(kept, find)
			keptTitles = append(keptTitles, title)
		}
	}
	return kept
}

// cleanTitle is a release title for comparing: case, punctuation and apostrophes ignored, "&" read as "and".
func cleanTitle(title string) string {
	var cleaned strings.Builder
	for _, r := range strings.ReplaceAll(strings.ToLower(title), "&", " and ") {
		switch {
		case r == '\'' || r == '\u2019' || r == '`':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cleaned.WriteRune(r)
		default:
			cleaned.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(cleaned.String()), " ")
}

func sameSize(a, b int64) bool {
	if a <= 0 || b <= 0 {
		return a <= 0 && b <= 0
	}
	larger := float64(max(a, b))
	return (larger-float64(min(a, b)))/larger <= duplicateSizeTolerance
}

// ProtocolFor maps an indexer implementation to candidate.protocol's vocabulary.
func ProtocolFor(impl string) string { return indexer.ProtocolOf(impl) }

// ProtocolDelay is a delay profile (D16): protocol preference as a wait, never a filter. Within the window after a
// release was posted only the preferred protocol is acted on; the other's accepted releases are held as DEFER.
type ProtocolDelay struct {
	Preferred string
	Window    time.Duration
	Now       time.Time
	// FirstSeen answers when an indexer's releases were first seen, for the ones that carry no date; nil knows none.
	FirstSeen func(indexerID int64, guids []string) map[string]time.Time
}

// Apply defers the non-preferred protocol's accepted finds that are still inside the window.
func (d ProtocolDelay) Apply(finds []Find) {
	if d.Preferred == "" || d.Window <= 0 {
		return
	}
	firstSeen := d.firstSeen(finds)
	for index := range finds {
		find := &finds[index]
		if find.Decision.Outcome != model.OutcomeAccept || ProtocolFor(find.Conn.Impl) == d.Preferred {
			continue
		}
		// An undated release is as old as its first sighting, so its wait ends; one never seen waits one window.
		posted := find.Raw.PublishedAt
		if posted.IsZero() {
			posted = cmp.Or(firstSeen[find.Conn.ID][find.Raw.GUID], d.Now)
		}
		releasedAt := posted.Add(d.Window)
		if !d.Now.Before(releasedAt) {
			continue
		}
		find.Decision.Outcome = model.OutcomeDefer
		find.Decision.Deferrals = append(find.Decision.Deferrals, model.Deferral{
			Reason: "protocol-delay", RetryAfter: releasedAt,
			Detail: map[string]any{"preferred_protocol": d.Preferred, "minutes": int64(d.Window / time.Minute)},
		})
	}
}

// firstSeen looks up the first sighting of every undated release a delay could hold, one lookup per indexer.
func (d ProtocolDelay) firstSeen(finds []Find) map[int64]map[string]time.Time {
	if d.FirstSeen == nil {
		return nil
	}
	guids := map[int64][]string{}
	for _, find := range finds {
		if find.Decision.Outcome == model.OutcomeAccept && ProtocolFor(find.Conn.Impl) != d.Preferred && find.Raw.PublishedAt.IsZero() && find.Raw.GUID != "" {
			guids[find.Conn.ID] = append(guids[find.Conn.ID], find.Raw.GUID)
		}
	}
	seen := make(map[int64]map[string]time.Time, len(guids))
	for indexerID, list := range guids {
		seen[indexerID] = d.FirstSeen(indexerID, list)
	}
	return seen
}

// PersistAll upserts every release and records its decision against itemID, in one batch; a release RSS showed is
// also a sighting of the item. The decisions come back in the order of finds.
func PersistAll(
	ctx context.Context, candidates statistics.Candidates, sightings *library.Sightings, itemID, categoryID int64, finds []Find,
	source string, at time.Time,
) ([]library.DecisionDetail, error) {
	records := make([]statistics.FindRecord, len(finds))
	for index, find := range finds {
		records[index] = findRecord(itemID, categoryID, find, source, at)
	}
	details, err := candidates.RecordFinds(ctx, records)
	if err != nil {
		return nil, err
	}
	if sightings != nil {
		var newest int64
		for _, detail := range details {
			if detail.ReleaseCandidate.Source == "rss" {
				newest = max(newest, detail.ReleaseCandidate.FirstSeenAt)
			}
		}
		if newest > 0 {
			if err := sightings.RecordRSS(ctx, itemID, newest); err != nil {
				return nil, err
			}
		}
	}
	return details, nil
}

func findRecord(itemID, categoryID int64, find Find, source string, at time.Time) statistics.FindRecord {
	var sizeBytes *int64
	if find.Raw.SizeBytes > 0 {
		size := find.Raw.SizeBytes
		sizeBytes = &size
	}
	var seeders, leechers *int64
	if ProtocolFor(find.Conn.Impl) == "torrent" {
		s, l := int64(find.Raw.Seeders), int64(find.Raw.Peers)
		seeders, leechers = &s, &l
	}
	var publishedAt *int64
	if !find.Raw.PublishedAt.IsZero() {
		p := find.Raw.PublishedAt.Unix()
		publishedAt = &p
	}
	confidence := find.ParseConfidence
	matchConfidence := find.MatchConfidence
	if matchConfidence == 0 {
		matchConfidence = 1 - find.Distance
	}
	return statistics.FindRecord{
		Candidate: library.NewReleaseCandidate{
			IndexerID: find.Conn.ID, GUID: find.Raw.GUID, Title: find.Raw.Title,
			SizeBytes: sizeBytes, Seeders: seeders, Leechers: leechers, PublishedAt: publishedAt,
			Protocol: ProtocolFor(find.Conn.Impl), DownloadURL: sealed(find.Conn, find.Raw.Link),
			ParseConfidence: &confidence, Source: source,
			FirstSeenAt: at.Unix(), ExpiresAt: at.Add(RetentionDefault).Unix(),
		},
		Decision: library.NewDecision{
			DesiredItemID: itemID, CategoryID: categoryID, Outcome: string(find.Decision.Outcome),
			Score:           find.Decision.Score(),
			ComponentsJSON:  jsonOrEmptyArray(find.Decision.Components),
			RejectionsJSON:  jsonOrEmptyArray(find.Decision.Rejections),
			DeferralsJSON:   jsonOrEmptyArray(find.Decision.Deferrals),
			MatchConfidence: &matchConfidence, DecidedAt: at.Unix(),
		},
	}
}

func jsonOrEmptyArray(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil || string(encoded) == "null" {
		return "[]"
	}
	return string(encoded)
}

func sealed(conn Conn, link string) string {
	if conn.Seal == nil {
		return link
	}
	return conn.Seal(link)
}
