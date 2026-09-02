package model

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// ChainConfig tunes when RunChain stops early and when it refuses to guess. Both are
// confidence thresholds in the same [0,1] space Identification.Confidence uses;
// AcceptThreshold should be at least AmbiguityThreshold, or the chain could stop on a
// candidate it would then refuse to resolve.
type ChainConfig struct {
	// AcceptThreshold: once the best candidate found so far reaches this, later
	// (typically more expensive) strategies do not run at all.
	AcceptThreshold float64
	// AmbiguityThreshold: the merged best candidate must still reach this once the
	// chain finishes, or identification is not resolved automatically -- it goes to
	// the manual identification queue (F29) instead.
	AmbiguityThreshold float64
}

// ChainResult is what running the chain decided.
type ChainResult struct {
	// Best is nil when nothing cleared AmbiguityThreshold: route to F29 rather than
	// guess.
	Best *Identification
	// Candidates is every merged identification, best first -- what the manual
	// queue shows when Best is nil, and the explanation (ADR-10) when it is not.
	Candidates []Identification
}

// RunChain runs identifiers in Order() (lower first), stopping once a candidate
// clears AcceptThreshold, then merges what was found by identity and decides whether
// the best merged candidate is confident enough to resolve automatically. Core owns
// this; each Identifier only ever answers for itself (ADR-24).
func RunChain(ctx context.Context, identifiers []Identifier, f FileRef, cfg ChainConfig) (ChainResult, error) {
	ordered := slices.Clone(identifiers)
	slices.SortFunc(ordered, func(a, b Identifier) int { return cmp.Compare(a.Order(), b.Order()) })

	var collected []Identification
	for _, identifier := range ordered {
		found, err := identifier.Identify(ctx, f)
		if err != nil {
			return ChainResult{}, fmt.Errorf("model: identifier %q: %w", identifier.Name(), err)
		}
		collected = append(collected, found...)

		if highestConfidence(collected) >= cfg.AcceptThreshold {
			break
		}
	}

	merged := mergeByIdentity(collected)
	slices.SortFunc(merged, func(a, b Identification) int {
		return cmp.Compare(b.Confidence, a.Confidence) // descending: best first
	})

	result := ChainResult{Candidates: merged}
	if len(merged) > 0 && merged[0].Confidence >= cfg.AmbiguityThreshold {
		result.Best = &merged[0]
	}
	return result, nil
}

func highestConfidence(found []Identification) float64 {
	var best float64
	for _, id := range found {
		if id.Confidence > best {
			best = id.Confidence
		}
	}
	return best
}

// identityKey groups Identifications that name the same thing, so two strategies
// agreeing strengthens one candidate instead of producing two.
type identityKey struct {
	provider, external string // set when the group key is an ExternalID
	workID             int64  // set when the group key is a WorkID
	unique             int    // set when neither is known -- never merges with anything
}

// keyFor prefers ExternalID over WorkID as the merge key: two strategies can agree
// on the same provider id while only one of them has resolved it to a local work, and
// they must still merge into one candidate rather than two.
func keyFor(id Identification, index int) identityKey {
	if id.ExternalID != nil {
		return identityKey{provider: id.ExternalID.Provider, external: id.ExternalID.ExternalID}
	}
	if id.WorkID != nil {
		return identityKey{workID: *id.WorkID}
	}
	return identityKey{unique: index}
}

// mergeByIdentity collects candidates, merges by external id (falling back to work
// id), and takes the highest confidence -- ADR-24's decision, in order.
func mergeByIdentity(found []Identification) []Identification {
	order := make([]identityKey, 0, len(found))
	groups := make(map[identityKey][]Identification, len(found))

	for index, id := range found {
		key := keyFor(id, index)
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], id)
	}

	merged := make([]Identification, 0, len(order))
	for _, key := range order {
		merged = append(merged, mergeGroup(groups[key]))
	}
	return merged
}

// mergeGroup combines identifications that already agree on identity. Strategy,
// WorkID, ExternalID and Confidence come from whichever member had the highest
// confidence -- the strategy that was most sure is the one worth trusting for the
// specifics -- but Evidence concatenates every member's, so the explanation shows
// everything that agreed, not only the winner's reason.
func mergeGroup(group []Identification) Identification {
	winner := group[0]
	for _, id := range group[1:] {
		if id.Confidence > winner.Confidence {
			winner = id
		}
	}

	merged := winner
	merged.Evidence = nil
	for _, id := range group {
		merged.Evidence = append(merged.Evidence, id.Evidence...)
	}
	return merged
}
