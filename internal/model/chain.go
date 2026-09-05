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
	return RunChainForFiles(ctx, identifiers, []FileRef{f}, cfg)
}

// RunChainForFiles is RunChain generalised to several files that are already known to
// belong together (ADR-48's IDENTIFY step: "the ADR-24 chain, per unit, independently"
// -- a unit is a group of files, and this runs the chain once for the whole group
// rather than once per file and merging the results afterward). Each identifier in
// Order() runs against every file before the next identifier runs at all, so a cheap
// strategy that resolves confidently from any one file in the unit still skips a more
// expensive strategy for the rest of the unit -- the same early-stop guarantee RunChain
// gives a single file. With one file this is byte-for-byte RunChain's old behaviour.
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
				// A unit's several files: one unreadable file (corrupt, not
				// actually audio) must not sink identification for the rest --
				// the same "absent, not present-and-empty" reasoning
				// consumer.go already applies to a failed probe.
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

// strictlyBest requires the top merged candidate to strictly outrank the runner-up,
// not merely lead a tie. A single file can only ever produce one opinion per
// confidence tier (each Identifier reads that file once), so this cannot fire for
// RunChain's original one-file case except when two different strategies coincide
// on the same float, arguably a bug either way. RunChainForFiles' whole point is
// several files' independent opinions in one merge, and two files embedding
// different, genuinely valid identities at the same tier (two release-group MBIDs
// that disagree) tie exactly -- ADR-48's own rule for GROUP applies here too:
// "disagreement between signals lowers confidence; it does not pick a winner
// silently."
func strictlyBest(merged []Identification) bool {
	return len(merged) == 1 || merged[0].Confidence > merged[1].Confidence
}

// MergeIdentifications merges identifications by identity (external id, falling back
// to work id) and ranks the merged result best first -- the same rule RunChain/
// RunChainForFiles apply to what one chain run collected, exposed so a caller with
// identifications gathered some other way (there is none yet) gets the identical
// merge behaviour rather than a second implementation of it.
func MergeIdentifications(found []Identification) []Identification {
	merged := mergeByIdentity(found)
	slices.SortFunc(merged, func(a, b Identification) int {
		return cmp.Compare(b.Confidence, a.Confidence) // descending: best first
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
