package settings_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

// Nothing stored yet reads as ADR-48's own default, "Confident" (0.90) -- an
// absent policy row means "use the shipped default", not zero.
func TestImportAutoCommitConfidence_DefaultsWhenUnset(t *testing.T) {
	cache, _ := newCache(t)

	if got := cache.Snapshot().ImportAutoCommitConfidence(); got != settings.DefaultImportAutoCommitConfidence {
		t.Errorf("ImportAutoCommitConfidence() = %v, want the default %v",
			got, settings.DefaultImportAutoCommitConfidence)
	}
}

func TestSetImportAutoCommitConfidence_RoundTrips(t *testing.T) {
	cache, cl := newCache(t)

	if err := cache.SetImportAutoCommitConfidence(t.Context(), cl, 0.75); err != nil {
		t.Fatalf("SetImportAutoCommitConfidence: %v", err)
	}
	if got := cache.Snapshot().ImportAutoCommitConfidence(); got != 0.75 {
		t.Errorf("ImportAutoCommitConfidence() = %v, want 0.75", got)
	}
}

// architecture.md's own table explicitly welcomes "a raw number ... for anyone
// who wants one" beyond the five named levels -- 0.82 is not one of them and must
// still be accepted.
func TestSetImportAutoCommitConfidence_AcceptsARawNumber(t *testing.T) {
	cache, cl := newCache(t)

	if err := cache.SetImportAutoCommitConfidence(t.Context(), cl, 0.82); err != nil {
		t.Fatalf("SetImportAutoCommitConfidence: %v", err)
	}
	if got := cache.Snapshot().ImportAutoCommitConfidence(); got != 0.82 {
		t.Errorf("ImportAutoCommitConfidence() = %v, want 0.82", got)
	}
}

func TestSetImportAutoCommitConfidence_RejectsOutOfRange(t *testing.T) {
	cache, cl := newCache(t)

	for _, bad := range []float64{-0.01, 1.01, -1, 2} {
		if err := cache.SetImportAutoCommitConfidence(t.Context(), cl, bad); err == nil {
			t.Errorf("SetImportAutoCommitConfidence(%v) succeeded, want a validation error", bad)
		}
	}
	if got := cache.Snapshot().ImportAutoCommitConfidence(); got != settings.DefaultImportAutoCommitConfidence {
		t.Errorf("ImportAutoCommitConfidence() = %v after rejected writes, want the default unchanged", got)
	}
}

// Nothing stored yet reads as the shipped ladder, not an empty one: an absent
// row must never mean "place nothing".
func TestImportTransferLadder_DefaultsWhenUnset(t *testing.T) {
	cache, _ := newCache(t)

	got := cache.Snapshot().ImportTransferLadder()
	want := settings.DefaultImportTransferLadder
	if len(got) != len(want) {
		t.Fatalf("ImportTransferLadder() = %v, want the default %v", got, want)
	}
	for index, method := range want {
		if got[index] != method {
			t.Fatalf("ImportTransferLadder() = %v, want the default %v", got, want)
		}
	}
}

// The stored order is the order read back -- the whole point of the setting is
// that the user's own priority survives, including the rungs that are absent
// from the default.
func TestSetImportTransferLadder_RoundTripsInOrder(t *testing.T) {
	cache, cl := newCache(t)

	ladder := []string{settings.TransferHardlink, settings.TransferMove, settings.TransferSymlink}
	if err := cache.SetImportTransferLadder(t.Context(), cl, ladder); err != nil {
		t.Fatalf("SetImportTransferLadder: %v", err)
	}
	got := cache.Snapshot().ImportTransferLadder()
	if len(got) != len(ladder) {
		t.Fatalf("ImportTransferLadder() = %v, want %v", got, ladder)
	}
	for index, method := range ladder {
		if got[index] != method {
			t.Fatalf("ImportTransferLadder() = %v, want %v", got, ladder)
		}
	}
}

func TestSetImportTransferLadder_RejectsAnUnknownMethod(t *testing.T) {
	cache, cl := newCache(t)

	err := cache.SetImportTransferLadder(t.Context(), cl, []string{settings.TransferHardlink, "teleport"})
	if err == nil {
		t.Fatal("SetImportTransferLadder accepted an unknown method, want a validation error")
	}
}

// An empty ladder is a user clearing every method, which is impossible to
// honour -- refused rather than silently answered with the default.
func TestSetImportTransferLadder_RejectsAnEmptyLadder(t *testing.T) {
	cache, cl := newCache(t)

	if err := cache.SetImportTransferLadder(t.Context(), cl, nil); err == nil {
		t.Fatal("SetImportTransferLadder accepted an empty ladder, want a validation error")
	}
}

// A method named twice is a confused ladder, not a harmless one: the second
// occurrence can never be reached.
func TestSetImportTransferLadder_RejectsADuplicateMethod(t *testing.T) {
	cache, cl := newCache(t)

	ladder := []string{settings.TransferHardlink, settings.TransferCopy, settings.TransferHardlink}
	if err := cache.SetImportTransferLadder(t.Context(), cl, ladder); err == nil {
		t.Fatal("SetImportTransferLadder accepted a duplicate method, want a validation error")
	}
}
