package model_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/identify/model"
)

// fakeIdentifier is a strategy stub: it returns whatever the test built and counts
// how many times it was asked, so the early-stop behaviour is verifiable rather than
// inferred from timing.
type fakeIdentifier struct {
	name    string
	order   int
	results []model.Identification
	err     error
	calls   *int
}

func (f fakeIdentifier) Name() string { return f.name }
func (f fakeIdentifier) Order() int   { return f.order }

func (f fakeIdentifier) Identify(context.Context, model.FileRef) ([]model.Identification, error) {
	if f.calls != nil {
		*f.calls++
	}
	return f.results, f.err
}

func workID(id int64) *int64 { return &id }

func externalID(provider, id string) *model.ExternalID {
	return &model.ExternalID{Provider: provider, ExternalID: id}
}

func runChain(t *testing.T, identifiers []model.Identifier, cfg model.ChainConfig) model.ChainResult {
	t.Helper()
	result, err := model.RunChain(t.Context(), identifiers, model.FileRef{}, cfg)
	if err != nil {
		t.Fatalf("RunChain: %v", err)
	}
	return result
}

// The chain runs Order() ascending, and a later, more expensive strategy must not
// run at all once an earlier one is already confident enough -- that is the entire
// reason Order exists.
func TestRunChain_StopsRunningOnceAcceptThresholdClears(t *testing.T) {
	var expensiveCalls int
	cheap := fakeIdentifier{name: "hash", order: 0, results: []model.Identification{
		{Strategy: "hash", WorkID: workID(1), Confidence: 0.99},
	}}
	expensive := fakeIdentifier{name: "fingerprint", order: 1, calls: &expensiveCalls, results: []model.Identification{
		{Strategy: "fingerprint", WorkID: workID(1), Confidence: 0.5},
	}}

	result := runChain(t, []model.Identifier{expensive, cheap}, model.ChainConfig{
		AcceptThreshold: 0.9, AmbiguityThreshold: 0.5,
	})

	if expensiveCalls != 0 {
		t.Errorf("the expensive strategy ran %d times, want 0: the cheap one already cleared the threshold", expensiveCalls)
	}
	if result.Best == nil || *result.Best.WorkID != 1 || result.Best.Confidence != 0.99 {
		t.Errorf("Best = %+v, want the cheap strategy's result", result.Best)
	}
}

// Order is a run order, not a priority label: registering identifiers out of order
// must not change which one runs first. Confidence 0 never clears AcceptThreshold,
// so every strategy runs and the call sequence records the true order.
func TestRunChain_RunsInOrderRegardlessOfRegistrationOrder(t *testing.T) {
	var seen []string
	step := func(name string, order int) model.Identifier {
		return trackingIdentifier{
			base: fakeIdentifier{name: name, order: order,
				results: []model.Identification{{Strategy: name, Confidence: 0}}},
			seen: &seen,
		}
	}

	_, err := model.RunChain(t.Context(),
		[]model.Identifier{step("third", 2), step("first", 0), step("second", 1)},
		model.FileRef{}, model.ChainConfig{AcceptThreshold: 1, AmbiguityThreshold: 1})
	if err != nil {
		t.Fatalf("RunChain: %v", err)
	}

	want := []string{"first", "second", "third"}
	if len(seen) != len(want) {
		t.Fatalf("call sequence = %v, want %v", seen, want)
	}
	for index, name := range want {
		if seen[index] != name {
			t.Errorf("call sequence = %v, want %v", seen, want)
			break
		}
	}
}

// trackingIdentifier wraps a fakeIdentifier to append to a shared call-order log --
// separate from fakeIdentifier's own counter, which only counts, not orders.
type trackingIdentifier struct {
	base fakeIdentifier
	seen *[]string
}

func (t trackingIdentifier) Name() string { return t.base.Name() }
func (t trackingIdentifier) Order() int   { return t.base.Order() }
func (t trackingIdentifier) Identify(ctx context.Context, f model.FileRef) ([]model.Identification, error) {
	*t.seen = append(*t.seen, t.base.Name())
	return t.base.Identify(ctx, f)
}

// Two strategies naming the same external id must merge into one candidate, not
// crowd the results with the same work twice.
func TestRunChain_MergesOnMatchingExternalID(t *testing.T) {
	tag := fakeIdentifier{name: "embedded-tag", order: 0, results: []model.Identification{
		{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.6,
			Evidence: []model.MatchEvidence{{Kind: "embedded_tag", Detail: "MUSICBRAINZ_TRACKID"}}},
	}}
	fingerprint := fakeIdentifier{name: "fingerprint", order: 1, results: []model.Identification{
		{Strategy: "fingerprint", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.8,
			Evidence: []model.MatchEvidence{{Kind: "fingerprint", Detail: "acoustid match"}}},
	}}

	result := runChain(t, []model.Identifier{tag, fingerprint}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if len(result.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(result.Candidates), result.Candidates)
	}
	merged := result.Candidates[0]
	if merged.Confidence != 0.8 {
		t.Errorf("merged confidence = %v, want the higher of the two (0.8)", merged.Confidence)
	}
	if merged.Strategy != "fingerprint" {
		t.Errorf("merged strategy = %q, want the higher-confidence strategy's name", merged.Strategy)
	}
	if len(merged.Evidence) != 2 {
		t.Errorf("merged evidence has %d entries, want both strategies' evidence kept: %+v",
			len(merged.Evidence), merged.Evidence)
	}
}

// A strategy that already knows the work id and one that only has an external id
// must still merge when they in fact agree, or the same file identifies as "two
// different things" depending on which strategy is asked.
func TestRunChain_MergesOnMatchingWorkID(t *testing.T) {
	provenance := fakeIdentifier{name: "provenance", order: 0, results: []model.Identification{
		{Strategy: "provenance", WorkID: workID(42), Confidence: 0.7},
	}}
	parse := fakeIdentifier{name: "parse", order: 1, results: []model.Identification{
		{Strategy: "parse", WorkID: workID(42), Confidence: 0.4},
	}}

	result := runChain(t, []model.Identifier{provenance, parse}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if len(result.Candidates) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(result.Candidates), result.Candidates)
	}
	if result.Candidates[0].Confidence != 0.7 {
		t.Errorf("confidence = %v, want 0.7", result.Candidates[0].Confidence)
	}
}

// Two strategies that genuinely disagree must stay two candidates -- merging them
// would hide a real conflict rather than surface it to F29.
func TestRunChain_KeepsGenuinelyDifferentCandidatesSeparate(t *testing.T) {
	a := fakeIdentifier{name: "a", order: 0, results: []model.Identification{
		{Strategy: "a", WorkID: workID(1), Confidence: 0.6},
	}}
	b := fakeIdentifier{name: "b", order: 1, results: []model.Identification{
		{Strategy: "b", WorkID: workID(2), Confidence: 0.55},
	}}

	result := runChain(t, []model.Identifier{a, b}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if len(result.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2: %+v", len(result.Candidates), result.Candidates)
	}
	if *result.Candidates[0].WorkID != 1 || *result.Candidates[1].WorkID != 2 {
		t.Errorf("candidates = %+v, want work 1 ranked before work 2", result.Candidates)
	}
}

// Below AmbiguityThreshold, RunChain must not guess: Best stays nil even though
// Candidates has something in it, so the caller can tell "found nothing" apart from
// "found something too uncertain to trust."
func TestRunChain_RefusesToResolveBelowAmbiguityThreshold(t *testing.T) {
	weak := fakeIdentifier{name: "parse", order: 0, results: []model.Identification{
		{Strategy: "parse", WorkID: workID(1), Confidence: 0.3},
	}}

	result := runChain(t, []model.Identifier{weak}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if result.Best != nil {
		t.Errorf("Best = %+v, want nil: confidence 0.3 is below the 0.5 ambiguity threshold", result.Best)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("got %d candidates, want the weak match still listed for the manual queue", len(result.Candidates))
	}
}

// Two genuinely different candidates that tie exactly must not resolve to whichever
// happens to sort first -- ADR-48's own rule for GROUP ("disagreement between
// signals lowers confidence; it does not pick a winner silently") applies to
// IDENTIFY's own merge too. A single RunChain file cannot produce this on its own
// (each Identifier reads that one file once); this is what RunChainForFiles' several-
// files case makes newly possible and newly necessary to guard.
func TestRunChain_RefusesToResolveATieBetweenDifferentCandidates(t *testing.T) {
	a := fakeIdentifier{name: "a", order: 0, results: []model.Identification{
		{Strategy: "a", WorkID: workID(1), Confidence: 0.9},
	}}
	b := fakeIdentifier{name: "b", order: 1, results: []model.Identification{
		{Strategy: "b", WorkID: workID(2), Confidence: 0.9},
	}}

	result := runChain(t, []model.Identifier{a, b}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if result.Best != nil {
		t.Errorf("Best = %+v, want nil: two different candidates tied at 0.9", result.Best)
	}
	if len(result.Candidates) != 2 {
		t.Errorf("got %d candidates, want both tied candidates kept for the queue", len(result.Candidates))
	}
}

// No strategy finding anything is not an error -- it is exactly what routes to F29.
func TestRunChain_HandlesNoIdentificationsAtAll(t *testing.T) {
	nothing := fakeIdentifier{name: "hash", order: 0, results: nil}

	result := runChain(t, []model.Identifier{nothing}, model.ChainConfig{
		AcceptThreshold: 1, AmbiguityThreshold: 0.5,
	})

	if result.Best != nil {
		t.Errorf("Best = %+v, want nil", result.Best)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("got %d candidates, want 0", len(result.Candidates))
	}
}

// A live run against real, messy download content surfaced this: one corrupt file's
// probe failure must not sink identification for the rest of the unit. RunChain's
// own single-file contract is untouched (the next test pins that); this is what
// RunChainForFiles must do differently once there is more than one file to answer
// for.
func TestRunChainForFiles_OneUnreadableFileDoesNotSinkTheWholeUnit(t *testing.T) {
	tag := perFileIdentifier{name: "embedded-tag", order: 0, byPath: map[string][]model.Identification{
		"02.flac": {{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.9}},
	}}
	// perFileIdentifier never errors on its own; simulate a corrupt file's read
	// failure with a wrapper that errors for exactly one path.
	failing := failOnPath{path: "01.flac", err: errors.New("moov atom not found"), inner: tag}

	files := []model.FileRef{{RelPath: "01.flac"}, {RelPath: "02.flac"}}
	result, err := model.RunChainForFiles(t.Context(), []model.Identifier{failing}, files,
		model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.6})
	if err != nil {
		t.Fatalf("RunChainForFiles: %v, want the readable file's result despite the other failing", err)
	}
	if result.Best == nil || result.Best.ExternalID.ExternalID != "rg-1" {
		t.Errorf("Best = %+v, want rg-1 from the one readable file", result.Best)
	}
}

// failOnPath wraps an Identifier to return a fixed error for one FileRef.RelPath and
// delegate everything else, so a single-file failure can be simulated without a full
// identifier fake.
type failOnPath struct {
	path  string
	err   error
	inner model.Identifier
}

func (f failOnPath) Name() string { return f.inner.Name() }
func (f failOnPath) Order() int   { return f.inner.Order() }
func (f failOnPath) Identify(ctx context.Context, file model.FileRef) ([]model.Identification, error) {
	if file.RelPath == f.path {
		return nil, f.err
	}
	return f.inner.Identify(ctx, file)
}

// A strategy failing outright -- a read error, not "found nothing" -- must stop the
// chain and name which strategy failed, or a transient I/O error looks identical to
// an honest "no match." RunChain's own single-file contract, unaffected by
// RunChainForFiles' multi-file tolerance above.
func TestRunChain_PropagatesAStrategyError(t *testing.T) {
	failing := fakeIdentifier{name: "fingerprint", order: 0, err: errors.New("read failed")}

	_, err := model.RunChain(t.Context(), []model.Identifier{failing}, model.FileRef{},
		model.ChainConfig{AcceptThreshold: 1, AmbiguityThreshold: 0.5})
	if err == nil {
		t.Fatal("RunChain: got nil error, want the strategy's error wrapped")
	}
	if !errors.Is(err, failing.err) {
		t.Errorf("RunChain: %v, want it to wrap the strategy's own error", err)
	}
}

// perFileIdentifier answers differently per FileRef.RelPath, the shape a real
// per-unit identifier has (each track's own tags), unlike fakeIdentifier which
// ignores the FileRef entirely.
type perFileIdentifier struct {
	name   string
	order  int
	byPath map[string][]model.Identification
	calls  *int
}

func (p perFileIdentifier) Name() string { return p.name }
func (p perFileIdentifier) Order() int   { return p.order }
func (p perFileIdentifier) Identify(_ context.Context, f model.FileRef) ([]model.Identification, error) {
	if p.calls != nil {
		*p.calls++
	}
	return p.byPath[f.RelPath], nil
}

// ADR-48's IDENTIFY step runs the chain "per unit, independently" -- RunChainForFiles
// is that: several files already known to belong together, identified in one call.
// Files with no embedded id (track 3) must not prevent the ones that agree from
// merging into a single, corroborated candidate.
func TestRunChainForFiles_MergesAgreementAcrossFiles(t *testing.T) {
	tag := perFileIdentifier{name: "embedded-tag", order: 0, byPath: map[string][]model.Identification{
		"01.flac": {{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.9,
			Evidence: []model.MatchEvidence{{Kind: "embedded_tag", Detail: "01.flac"}}}},
		"02.flac": {{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.9,
			Evidence: []model.MatchEvidence{{Kind: "embedded_tag", Detail: "02.flac"}}}},
		// 03.flac carries no tags at all -- absent, not disagreeing.
	}}

	files := []model.FileRef{{RelPath: "01.flac"}, {RelPath: "02.flac"}, {RelPath: "03.flac"}}
	result, err := model.RunChainForFiles(t.Context(), []model.Identifier{tag}, files,
		model.ChainConfig{AcceptThreshold: 1, AmbiguityThreshold: 0.5})
	if err != nil {
		t.Fatalf("RunChainForFiles: %v", err)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("got %d candidates, want the two agreeing files merged into one: %+v",
			len(result.Candidates), result.Candidates)
	}
	if result.Best == nil || result.Best.ExternalID.ExternalID != "rg-1" {
		t.Errorf("Best = %+v, want rg-1", result.Best)
	}
	// Both files' agreement is kept as evidence for the merged candidate -- this
	// pass does not yet turn agreement count into a confidence boost, but the raw
	// corroboration must survive the merge for a later pass to use.
	if len(result.Best.Evidence) != 2 {
		t.Errorf("merged evidence has %d entries, want both files': %+v",
			len(result.Best.Evidence), result.Best.Evidence)
	}
}

// A cheap strategy resolving confidently from any single file in the unit must skip
// a more expensive strategy for the rest of the unit too -- the same early-stop
// RunChain already gives one file, generalised across every file in the group.
func TestRunChainForFiles_StopsAcrossFilesOnceAcceptThresholdClears(t *testing.T) {
	var expensiveCalls int
	cheap := perFileIdentifier{name: "hash", order: 0, byPath: map[string][]model.Identification{
		"01.flac": {{Strategy: "hash", WorkID: workID(1), Confidence: 0.99}},
	}}
	expensive := perFileIdentifier{name: "fingerprint", order: 1, calls: &expensiveCalls}

	files := []model.FileRef{{RelPath: "01.flac"}, {RelPath: "02.flac"}}
	result, err := model.RunChainForFiles(t.Context(), []model.Identifier{expensive, cheap}, files,
		model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.5})
	if err != nil {
		t.Fatalf("RunChainForFiles: %v", err)
	}
	if expensiveCalls != 0 {
		t.Errorf("the expensive strategy ran %d times, want 0: one file already cleared the threshold", expensiveCalls)
	}
	if result.Best == nil || *result.Best.WorkID != 1 {
		t.Errorf("Best = %+v, want work 1 from the one file that resolved", result.Best)
	}
}

// Files that genuinely disagree (two different embedded ids in the same unit) must
// stay two separate candidates -- exactly the ambiguity F29's manual queue exists
// to surface, not something a per-unit merge should paper over.
func TestRunChainForFiles_KeepsGenuinelyDisagreeingFilesSeparate(t *testing.T) {
	tag := perFileIdentifier{name: "embedded-tag", order: 0, byPath: map[string][]model.Identification{
		"01.flac": {{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-1"), Confidence: 0.6}},
		"02.flac": {{Strategy: "embedded-tag", ExternalID: externalID("musicbrainz", "rg-2"), Confidence: 0.55}},
	}}

	files := []model.FileRef{{RelPath: "01.flac"}, {RelPath: "02.flac"}}
	result, err := model.RunChainForFiles(t.Context(), []model.Identifier{tag}, files,
		model.ChainConfig{AcceptThreshold: 1, AmbiguityThreshold: 0.5})
	if err != nil {
		t.Fatalf("RunChainForFiles: %v", err)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2 for two disagreeing files: %+v",
			len(result.Candidates), result.Candidates)
	}
}
