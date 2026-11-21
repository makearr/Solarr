package app

import "testing"

func TestLocksFor_OneLockPerMediaTypeSharedByEveryCaller(t *testing.T) {
	p := &process{}
	first := p.locksFor("movie")
	if first != p.locksFor("movie") {
		t.Error("two callers of one media type got different locks")
	}
	if first == p.locksFor("tv") {
		t.Error("two media types share one lock")
	}
}
