// Package placement implements section 11.7: choosing which root folder a
// new item lands on. Core decides which disk; the media module decides only
// the path within it (internal/moving/naming's job). Pure logic, no I/O -- the
// caller fetches candidates from settings.db and commits the chosen
// affinity back, the same separation internal/identify/score keeps from
// internal/db/settings.
package placement

import (
	"fmt"
	"sort"
)

// Strategy ranks the candidates that survive the CANDIDATES filter, chosen
// per category by the user.
type Strategy string

// The six strategies section 11.7's own table names.
const (
	StrategyMostFreeSpace   Strategy = "most_free_space"
	StrategyMostFreePercent Strategy = "most_free_percent"
	StrategyFillOrder       Strategy = "fill_order"
	StrategyRoundRobin      Strategy = "round_robin"
	StrategyWeighted        Strategy = "weighted"
	StrategyManual          Strategy = "manual"
)

// AffinityScope names what an affinity key binds at -- section 11.7's table.
// Resolve itself treats the key as an opaque string; the scope is what the
// caller uses to decide how to build one from a DesiredItem, so it lives
// here only as shared vocabulary for settings validation.
type AffinityScope string

// The seven scopes section 11.7's own table names.
const (
	ScopeAuto   AffinityScope = "auto"
	ScopeSeries AffinityScope = "series"
	ScopeSeason AffinityScope = "season"
	ScopeArtist AffinityScope = "artist"
	ScopeAlbum  AffinityScope = "album"
	ScopeWork   AffinityScope = "work"
	ScopeOff    AffinityScope = "off"
)

// RootCandidate is one root folder's state, as the caller last read it from
// settings.db -- Resolve trusts these values rather than probing anything
// itself; a stale reading is the caller's problem to refresh before calling
// (section 11.7: "immediately before any placement decision involving a
// root whose reading is stale").
type RootCandidate struct {
	RootFolderID   int64
	FSID           string
	Online         bool
	Writable       bool
	ReadOnly       bool
	AcceptsNew     bool
	FreeBytes      int64
	TotalBytes     int64
	MinFreeBytes   int64
	Ord            int64
	Weight         int64
	PlacementCount int64
}

// Request is what Resolve needs to decide -- Placement's own fields
// (section 11.7's Go sketch) plus the strategy and tiebreak inputs a real
// caller must supply, since this package has no settings.db of its own to
// read them from.
type Request struct {
	CategoryID      int64
	SizeBytes       int64
	AffinityEnabled bool
	AffinityKey     string
	// ExistingAffinityRoot is the root_folder_id the caller already looked up
	// in placement_affinity for AffinityKey, nil when there is no recorded
	// affinity yet -- Resolve is pure, so the lookup itself is the caller's
	// job (the same separation internal/identify/score keeps from internal/db/settings).
	ExistingAffinityRoot *int64
	Strategy             Strategy
	// SourceFSID is the download's own filesystem id, for the tiebreak that
	// prefers a root reflink/hardlink can reach instead of a copy (ADR-11).
	SourceFSID string
	// Manual is the user's own pre-chosen root for StrategyManual. Nil means
	// an automatic addition, which falls back to most_free_space.
	Manual *int64
	// Random answers a float64 in [0,1) for StrategyWeighted's proportional
	// pick -- injected rather than a package-global math/rand source, so a
	// test can pin the outcome instead of asserting only "some candidate".
	Random func() float64
}

// Outcome is what Resolve decided.
type Outcome string

// The two outcomes Resolve can reach.
const (
	OutcomePlaced Outcome = "PLACED"
	OutcomeDefer  Outcome = "DEFER"
)

// RejectedRoot is one candidate that did not qualify, and why -- so a DEFER
// names every root and its reason rather than just failing (section 11.7's
// own worked example).
type RejectedRoot struct {
	RootFolderID int64
	Reason       string
}

// Decision explains what Resolve chose, or why it deferred (ADR-10:
// structured data, no separate explain path). Its own shape, not
// model.Decision's -- that type is ADR-40's quality-scoring vocabulary
// (gates, tiers, additive score), which does not fit "why did this land on
// disk 2" any better than forcing a placement outcome through gates would.
type Decision struct {
	Outcome      Outcome
	AffinityUsed bool
	AffinityKey  string
	Strategy     Strategy
	Rejected     []RejectedRoot
	// Reason is set only for DEFER -- section 11.7's "structured reason, not
	// a crash".
	Reason string
}

// Resolve runs section 11.7's five-step order: affinity first (not a
// strategy), then the CANDIDATES filter, then the chosen Strategy ranks
// what is left, then the fs_id tiebreak. COMMIT (recording the choice in
// placement_affinity) is the caller's job -- Resolve is pure and commits
// nothing itself.
func Resolve(req Request, candidates []RootCandidate) (RootCandidate, Decision, error) {
	decision := Decision{Strategy: req.Strategy}

	if req.AffinityEnabled && req.AffinityKey != "" && req.ExistingAffinityRoot != nil {
		if chosen, ok := affinityCandidate(*req.ExistingAffinityRoot, req.SizeBytes, candidates); ok {
			decision.Outcome = OutcomePlaced
			decision.AffinityUsed = true
			decision.AffinityKey = req.AffinityKey
			return chosen, decision, nil
		}
	}

	eligible := make([]RootCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if qualifies(candidate, req.SizeBytes) {
			eligible = append(eligible, candidate)
			continue
		}
		decision.Rejected = append(decision.Rejected, RejectedRoot{
			RootFolderID: candidate.RootFolderID, Reason: rejectReason(candidate, req.SizeBytes),
		})
	}
	if len(eligible) == 0 {
		decision.Outcome = OutcomeDefer
		decision.Reason = fmt.Sprintf("no root folder available for category %d (needs %d bytes)", req.CategoryID, req.SizeBytes)
		return RootCandidate{}, decision, nil
	}

	ranked := rank(req, eligible)
	chosen := tiebreak(ranked, req.SourceFSID)

	decision.Outcome = OutcomePlaced
	return chosen, decision, nil
}

func affinityCandidate(rootFolderID, sizeBytes int64, candidates []RootCandidate) (RootCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.RootFolderID == rootFolderID && qualifies(candidate, sizeBytes) {
			return candidate, true
		}
	}
	return RootCandidate{}, false
}

func qualifies(c RootCandidate, sizeBytes int64) bool {
	return c.Online && c.Writable && !c.ReadOnly && c.AcceptsNew && c.FreeBytes-sizeBytes >= c.MinFreeBytes
}

func rejectReason(c RootCandidate, sizeBytes int64) string {
	switch {
	case !c.Online:
		return "offline"
	case !c.Writable:
		return "not writable"
	case c.ReadOnly:
		return "read-only"
	case !c.AcceptsNew:
		return "draining (accepts_new = 0)"
	default:
		return fmt.Sprintf("%d bytes free after this item, reserve %d", c.FreeBytes-sizeBytes, c.MinFreeBytes)
	}
}

func rank(req Request, candidates []RootCandidate) []RootCandidate {
	ranked := make([]RootCandidate, len(candidates))
	copy(ranked, candidates)

	switch req.Strategy {
	case StrategyMostFreePercent:
		sort.SliceStable(ranked, func(i, j int) bool { return freePercent(ranked[i]) > freePercent(ranked[j]) })
	case StrategyFillOrder:
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Ord < ranked[j].Ord })
	case StrategyRoundRobin:
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].PlacementCount < ranked[j].PlacementCount })
	case StrategyWeighted:
		return []RootCandidate{weightedPick(ranked, req.Random)}
	case StrategyManual:
		if req.Manual != nil {
			for _, candidate := range ranked {
				if candidate.RootFolderID == *req.Manual {
					return []RootCandidate{candidate}
				}
			}
		}
		// An automatic addition with no user present: falls back to
		// most_free_space (section 11.7's own stated default).
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].FreeBytes > ranked[j].FreeBytes })
	default: // StrategyMostFreeSpace, and the fallback for an unrecognised value
		sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].FreeBytes > ranked[j].FreeBytes })
	}
	return ranked
}

func freePercent(c RootCandidate) float64 {
	if c.TotalBytes <= 0 {
		return 0
	}
	return float64(c.FreeBytes) / float64(c.TotalBytes)
}

// weightedPick chooses proportionally to Weight -- a zero total (every
// candidate weighted 0) falls back to the first candidate rather than
// dividing by zero.
func weightedPick(candidates []RootCandidate, random func() float64) RootCandidate {
	var total int64
	for _, candidate := range candidates {
		total += candidate.Weight
	}
	if total <= 0 || random == nil {
		return candidates[0]
	}
	target := random() * float64(total)
	var cumulative float64
	for _, candidate := range candidates {
		cumulative += float64(candidate.Weight)
		if target < cumulative {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

// tiebreak prefers a root sharing the download's own filesystem id, so
// import can reflink or hardlink instead of copying (ADR-11) -- applied
// only among candidates already tied at the front of the strategy's own
// ranking, never overriding the strategy's choice of best candidate.
func tiebreak(ranked []RootCandidate, sourceFSID string) RootCandidate {
	if sourceFSID == "" || len(ranked) == 0 {
		return ranked[0]
	}
	for _, candidate := range ranked {
		if candidate.FSID == sourceFSID {
			return candidate
		}
	}
	return ranked[0]
}
