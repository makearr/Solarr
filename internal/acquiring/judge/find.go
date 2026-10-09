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
// additive score, then the closer match.
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
		return cmp.Compare(a.Distance, b.Distance)
	})
}

// duplicateSizeTolerance is how far two indexers' sizes for one release may differ (within 1%).
const duplicateSizeTolerance = 0.01

// WithoutCrossIndexerDuplicates keeps one row for a release two indexers both list: same title once case and
// punctuation are ignored, size within 1%. Indexers are searched in priority order, so the first find is kept.
func WithoutCrossIndexerDuplicates(finds []Find) []Find {
	kept := finds[:0:0]
	for _, find := range finds {
		duplicate := slices.ContainsFunc(kept, func(earlier Find) bool {
			return earlier.Conn.ID != find.Conn.ID && cleanTitle(earlier.Raw.Title) == cleanTitle(find.Raw.Title) &&
				sameSize(earlier.Raw.SizeBytes, find.Raw.SizeBytes)
		})
		if !duplicate {
			kept = append(kept, find)
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
func ProtocolFor(impl string) string {
	if impl == "torznab" {
		return "torrent"
	}
	return "usenet"
}

// ProtocolDelay is a delay profile (D16): protocol preference as a wait, never a filter. Within the window after a
// release was posted only the preferred protocol is acted on; the other's accepted releases are held as DEFER.
type ProtocolDelay struct {
	Preferred string
	Window    time.Duration
	Now       time.Time
}

// Apply defers the non-preferred protocol's accepted finds that are still inside the window.
func (d ProtocolDelay) Apply(finds []Find) {
	if d.Preferred == "" || d.Window <= 0 {
		return
	}
	for index := range finds {
		find := &finds[index]
		if find.Decision.Outcome != model.OutcomeAccept || ProtocolFor(find.Conn.Impl) == d.Preferred {
			continue
		}
		posted := find.Raw.PublishedAt
		if posted.IsZero() {
			posted = d.Now
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

// Persist upserts the release and records the decision against itemID; a release RSS showed is also a sighting.
func Persist(
	ctx context.Context, candidates library.Candidates, sightings *library.Sightings, itemID int64, find Find, source string, at time.Time,
) (library.DecisionDetail, error) {
	var sizeBytes *int64
	if find.Raw.SizeBytes > 0 {
		size := find.Raw.SizeBytes
		sizeBytes = &size
	}
	var seeders, leechers *int64
	if find.Conn.Impl == "torznab" {
		s, l := int64(find.Raw.Seeders), int64(find.Raw.Peers)
		seeders, leechers = &s, &l
	}
	var publishedAt *int64
	if !find.Raw.PublishedAt.IsZero() {
		p := find.Raw.PublishedAt.Unix()
		publishedAt = &p
	}
	confidence := find.ParseConfidence
	candidate, err := candidates.Upsert(ctx, library.NewReleaseCandidate{
		IndexerID: find.Conn.ID, GUID: find.Raw.GUID, Title: find.Raw.Title,
		SizeBytes: sizeBytes, Seeders: seeders, Leechers: leechers, PublishedAt: publishedAt,
		Protocol: ProtocolFor(find.Conn.Impl), DownloadURL: find.Raw.Link,
		ParseConfidence: &confidence, Source: source,
		FirstSeenAt: at.Unix(), ExpiresAt: at.Add(RetentionDefault).Unix(),
	})
	if err != nil {
		return library.DecisionDetail{}, err
	}
	if sightings != nil && candidate.Source == "rss" {
		if err := sightings.RecordRSS(ctx, itemID, candidate.FirstSeenAt); err != nil {
			return library.DecisionDetail{}, err
		}
	}
	matchConfidence := find.MatchConfidence
	if matchConfidence == 0 {
		matchConfidence = 1 - find.Distance
	}
	decision, err := candidates.RecordDecision(ctx, library.NewDecision{
		CandidateID: candidate.ID, DesiredItemID: itemID, Outcome: string(find.Decision.Outcome),
		Score:           find.Decision.Score(),
		ComponentsJSON:  jsonOrEmptyArray(find.Decision.Components),
		RejectionsJSON:  jsonOrEmptyArray(find.Decision.Rejections),
		DeferralsJSON:   jsonOrEmptyArray(find.Decision.Deferrals),
		MatchConfidence: &matchConfidence, DecidedAt: at.Unix(),
	})
	if err != nil {
		return library.DecisionDetail{}, err
	}
	return library.DecisionDetail{Decision: decision, ReleaseCandidate: candidate}, nil
}

func jsonOrEmptyArray(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil || string(encoded) == "null" {
		return "[]"
	}
	return string(encoded)
}
