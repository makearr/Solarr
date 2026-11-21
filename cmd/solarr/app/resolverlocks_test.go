package app

import "testing"

func TestLocksFor_OneLockPerMediaTypeSharedByEveryCaller(t *testing.T) {
	p := &process{}
	if p.locksFor("movie") != p.locksFor("movie") {
		t.Error("two callers of one media type got different locks")
	}
	if p.locksFor("movie") == p.locksFor("tv") {
		t.Error("two media types share one lock")
	}
}
