// Package placement implements section 11.7: core picks the root folder for a new item (the media module picks the path within it); pure logic, the caller fetches candidates and commits the affinity.
package placement

import (
	"fmt"
	"sort"

	"github.com/makearr/Solarr/internal/db/settings"
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

// RootCandidate is one root's state as the caller last read it; Resolve trusts it, and refreshing a stale reading is the caller's job (section 11.7).
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

// CandidateOf is the one conversion of a configured root folder into a placement candidate; an unprobed free or total
// size counts as zero.
func CandidateOf(root settings.RootFolder, acceptsNew bool) RootCandidate {
	var freeBytes, totalBytes int64
	if root.FreeBytes != nil {
		freeBytes = *root.FreeBytes
	}
	if root.TotalBytes != nil {
		totalBytes = *root.TotalBytes
	}
	return RootCandidate{
		RootFolderID: root.ID, FSID: root.FSID, Online: root.Online, Writable: root.Writable, ReadOnly: root.ReadOnly,
		AcceptsNew: acceptsNew, FreeBytes: freeBytes, TotalBytes: totalBytes, MinFreeBytes: root.ReserveBytes(),
	}
}

// Request is what Resolve needs: Placement's fields (section 11.7) plus the strategy and tiebreak inputs, since this package has no settings.db.
type Request struct {
	CategoryID      int64
	SizeBytes       int64
	AffinityEnabled bool
	AffinityKey     string
	// ExistingAffinityRoot is the root_folder_id recorded in placement_affinity for AffinityKey, nil when none; the lookup is the caller's because Resolve is pure.
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

// Decision explains what Resolve chose or why it deferred (ADR-10: structured data), its own shape because model.Decision is ADR-40's quality-scoring vocabulary.
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

// Resolve runs section 11.7's order: affinity (not a strategy), the CANDIDATES filter, the Strategy ranking, then the fs_id tiebreak; COMMIT to placement_affinity is the caller's job.
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
	if !c.Online || !c.Writable || c.ReadOnly || !c.AcceptsNew || sizeBytes < 0 || c.FreeBytes < 0 || c.MinFreeBytes < 0 {
		return false
	}
	return sizeBytes <= c.FreeBytes && c.FreeBytes-sizeBytes >= c.MinFreeBytes
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
	case c.FreeBytes < 0 || c.MinFreeBytes < 0:
		return "invalid capacity reading"
	case sizeBytes < 0:
		return "invalid negative item size"
	case c.FreeBytes < sizeBytes:
		return fmt.Sprintf("%d bytes free, needs %d", c.FreeBytes, sizeBytes)
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

// tiebreak prefers a root sharing the download's filesystem id so import can reflink or hardlink (ADR-11), only among candidates tied at the front of the ranking.
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
