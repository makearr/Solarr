package settings_test

import (
	"testing"
)

func TestDiskGroupOverrides_RoundTripAndBlankDropsOne(t *testing.T) {
	cache, fake := newCache(t)
	ctx := t.Context()
	if got := cache.Snapshot().DiskGroupOverrides(); len(got) != 0 {
		t.Fatalf("fresh overrides = %v, want none", got)
	}
	if err := cache.SetDiskGroupOverrides(ctx, fake, map[string]string{"/a": " shared ", "/b": "  "}); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().DiskGroupOverrides(); len(got) != 1 || got["/a"] != "shared" {
		t.Fatalf("overrides = %v, want only /a=shared", got)
	}
	if err := cache.SetDiskGroupOverrides(ctx, fake, map[string]string{"relative": "x"}); err == nil {
		t.Fatal("a relative path must be refused")
	}
}

func TestScanSchedule_IntervalAndFingerprintsRoundTrip(t *testing.T) {
	cache, fake := newCache(t)
	ctx := t.Context()
	if got := cache.Snapshot().ScanInterval(); got != 0 {
		t.Fatalf("default interval = %v, want automatic (0)", got)
	}
	if err := cache.SetScanIntervalHours(ctx, fake, 12); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().ScanInterval(); got.Hours() != 12 {
		t.Fatalf("interval = %v, want 12h", got)
	}
	if err := cache.SetScanIntervalHours(ctx, fake, -1); err == nil {
		t.Fatal("a negative interval must be refused")
	}
	if err := cache.SetScanFingerprint(ctx, fake, 3, "used=10;entries=4"); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetScanFingerprint(ctx, fake, 5, "used=7;entries=1"); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().ScanFingerprints(); got[3] != "used=10;entries=4" || got[5] != "used=7;entries=1" {
		t.Fatalf("fingerprints = %v, want both roots kept", got)
	}
}
