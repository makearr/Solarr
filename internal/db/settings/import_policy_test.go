package settings_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/settings"
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
