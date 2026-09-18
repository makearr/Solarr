package music_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/acoustid"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

type fakeFingerprinter struct {
	print mediatool.Fingerprint
	// byPath answers per file, for a batch whose files are different recordings.
	byPath map[string]mediatool.Fingerprint
	err    error
	calls  int
}

func (f *fakeFingerprinter) Fingerprint(_ context.Context, paths []string) ([]mediatool.Fingerprint, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	prints := make([]mediatool.Fingerprint, len(paths))
	for i, one := range paths {
		if found, ok := f.byPath[one]; ok {
			prints[i] = found
			continue
		}
		prints[i] = f.print
	}
	return prints, nil
}

type fakeLookup struct {
	enabled bool
	matches []acoustid.Match
	// byPrint answers per fingerprint, the way the real service does.
	byPrint map[string][]acoustid.Match
	err     error
	calls   int
}

func (f *fakeLookup) Enabled() bool { return f.enabled }
func (f *fakeLookup) Lookup(_ context.Context, fingerprint string, _ time.Duration) ([]acoustid.Match, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.byPrint != nil {
		return f.byPrint[fingerprint], nil
	}
	return f.matches, nil
}

func newAcoustID(prints *fakeFingerprinter, lookup *fakeLookup) music.AcoustIDIdentifier {
	return music.NewAcoustIDIdentifier(music.AcoustIDIdentifierConfig{
		Fingerprints: prints, Lookup: lookup, Absolute: func(rel string) string { return "/music/" + rel },
	})
}

// With no key the rung is disabled: it answers nothing and, crucially, never
// decodes audio. Fingerprinting a whole library for a lookup that cannot
// happen would be the expensive half of a feature that is switched off.
func TestAcoustIDIdentifier_DisabledWithoutAKeyAndDoesNotFingerprint(t *testing.T) {
	prints := &fakeFingerprinter{}
	identifier := newAcoustID(prints, &fakeLookup{enabled: false})

	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a/b.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found = %+v, want nothing while disabled", found)
	}
	if prints.calls != 0 {
		t.Errorf("fingerprinted %d times while disabled, want 0", prints.calls)
	}
}

// It runs last: fingerprinting decodes audio and then makes a network call.
func TestAcoustIDIdentifier_RunsAfterEveryCheaperRung(t *testing.T) {
	identifier := newAcoustID(&fakeFingerprinter{}, &fakeLookup{})
	if identifier.Order() <= music.OrderParseSearch {
		t.Errorf("Order() = %d, want it after the text search (%d)",
			identifier.Order(), music.OrderParseSearch)
	}
}

func TestAcoustIDIdentifier_ReportsReleaseGroupsWithAcoustIDsOwnScore(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{Chromaprint: "AQAD", Duration: 3 * time.Minute}}
	lookup := &fakeLookup{enabled: true, matches: []acoustid.Match{
		{Score: 0.93, Recordings: []acoustid.Recording{{MBID: "rec-1", Title: "Sesoge"}},
			ReleaseGroupMBIDs: []string{"rg-1", "rg-2"}},
	}}
	identifier := newAcoustID(prints, lookup)

	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a/b.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found = %+v, want one per release group", found)
	}
	for _, identification := range found {
		if identification.Confidence != 0.93 {
			t.Errorf("Confidence = %v, want AcoustID's own score", identification.Confidence)
		}
		if identification.Confidence >= 1.0 {
			t.Error("a fingerprint match must never claim certainty (ADR-24)")
		}
		if identification.ExternalID == nil || identification.ExternalID.Provider != music.ProviderReleaseGroup {
			t.Errorf("ExternalID = %+v, want a release group", identification.ExternalID)
		}
	}
}

// A file that cannot be fingerprinted has no answer from this rung; it is not
// an error that should stop the chain.
func TestAcoustIDIdentifier_AFingerprintFailureIsNoAnswer(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{Err: errors.New("fpcalc exploded")}}
	lookup := &fakeLookup{enabled: true}
	identifier := newAcoustID(prints, lookup)

	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a/b.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found = %+v, want nothing", found)
	}
	if lookup.calls != 0 {
		t.Error("looked up a fingerprint that was never produced")
	}
}

// A partial fingerprint is still usable -- that is the whole reason it is kept.
func TestAcoustIDIdentifier_UsesAPartialFingerprint(t *testing.T) {
	prints := &fakeFingerprinter{print: mediatool.Fingerprint{
		Chromaprint: "AQAD", Duration: time.Minute, Partial: true,
	}}
	lookup := &fakeLookup{enabled: true, matches: []acoustid.Match{
		{Score: 0.88, ReleaseGroupMBIDs: []string{"rg-1"}},
	}}
	identifier := newAcoustID(prints, lookup)

	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a/b.flac"})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("found = %+v, want the partial fingerprint used", found)
	}
}
