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
