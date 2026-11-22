package model

import (
	"cmp"
	"context"
	"fmt"
	"slices"
)

// ChainConfig sets when RunChain stops early and when it refuses to guess; AcceptThreshold should be at least AmbiguityThreshold or the chain could stop on a candidate it then refuses to resolve.
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

// RunChain runs identifiers in Order(), stopping once a candidate clears AcceptThreshold, then merges by identity and decides whether the best is confident enough (ADR-24).
func RunChain(ctx context.Context, identifiers []Identifier, f FileRef, cfg ChainConfig) (ChainResult, error) {
	return RunChainForFiles(ctx, identifiers, []FileRef{f}, cfg)
}

// RunChainForFiles runs the chain once for a group of files (ADR-48 IDENTIFY): each identifier runs on every file before the next, so one confident file still skips costlier strategies; with one file it equals RunChain.
func RunChainForFiles(ctx context.Context, identifiers []Identifier, files []FileRef, cfg ChainConfig) (ChainResult, error) {
	ordered := slices.Clone(identifiers)
	slices.SortFunc(ordered, func(a, b Identifier) int { return cmp.Compare(a.Order(), b.Order()) })

	var collected []Identification
	for _, identifier := range ordered {
		for _, f := range files {
			found, err := identifier.Identify(ctx, f)
			if err != nil {
				if len(files) == 1 {
					// RunChain's own single-file contract, unchanged: a strategy
					// failing to run at all is the caller's problem to log and
					// skip (consumer.go does exactly that, one file at a time).
					return ChainResult{}, fmt.Errorf("model: identifier %q: %w", identifier.Name(), err)
				}
				// One unreadable file must not sink identification for the rest of a unit; treated as absent, like a failed probe in consumer.go.
				continue
			}
			collected = append(collected, found...)
		}

		if highestConfidence(collected) >= cfg.AcceptThreshold {
			break
		}
	}

	merged := MergeIdentifications(collected)

	result := ChainResult{Candidates: merged}
	if len(merged) > 0 && merged[0].Confidence >= cfg.AmbiguityThreshold && strictlyBest(merged) {
		result.Best = &merged[0]
	}
	return result, nil
}

// strictlyBest requires the top merged candidate to strictly outrank the runner-up: files embedding different valid identities at one tier tie, and ADR-48 says disagreement lowers confidence instead of picking a winner.
func strictlyBest(merged []Identification) bool {
	return len(merged) == 1 || outranks(merged[0], merged[1]) > 0
}

// outranks orders by precedence, then confidence: a guess never outranks an assertion,
// however confident the guess (2026-09-24). Positive when a is better.
func outranks(a, b Identification) int {
	if c := cmp.Compare(a.Precedence, b.Precedence); c != 0 {
		return c
	}
	return cmp.Compare(a.Confidence, b.Confidence)
}

// MergeIdentifications merges by identity (external id, else work id) and ranks best first, the rule RunChain applies, exposed so other callers need no second implementation.
func MergeIdentifications(found []Identification) []Identification {
	merged := mergeByIdentity(found)
	slices.SortStableFunc(merged, func(a, b Identification) int {
		return outranks(b, a) // descending: best first
	})
	return merged
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

// mergeGroup combines identifications that already agree on identity. The specifics come from
// the strongest member (precedence, then confidence); Evidence concatenates every member's,
// so the explanation shows everything that agreed, not only the winner's reason.
func mergeGroup(group []Identification) Identification {
	winner := group[0]
	for _, id := range group[1:] {
		if outranks(id, winner) > 0 {
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
