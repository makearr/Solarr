package score

import (
	"fmt"
	"testing"
)

func TestCompiledPatternCompilesOnce(t *testing.T) {
	const pattern = `^flac-once-[0-9]+$`
	before := patternCache.compiles
	first := CompiledPattern(pattern)
	second := CompiledPattern(pattern)
	if first == nil || first != second {
		t.Fatalf("second call did not return the cached pattern: %v %v", first, second)
	}
	if got := patternCache.compiles - before; got != 1 {
		t.Fatalf("compiles = %d, want 1", got)
	}
}

func TestCompiledPatternInvalidNeverMatches(t *testing.T) {
	if got := CompiledPattern(`([unclosed`); got != nil {
		t.Fatalf("invalid pattern compiled: %v", got)
	}
	if got := CompiledPattern(`([unclosed`); got != nil {
		t.Fatalf("invalid pattern compiled on second call: %v", got)
	}
}

func TestCompiledPatternCacheStaysBounded(t *testing.T) {
	for index := range 2 * maxCachedPatterns {
		CompiledPattern(fmt.Sprintf("^bounded-%d$", index))
	}
	patternCache.Lock()
	size := len(patternCache.compiled)
	patternCache.Unlock()
	if size > maxCachedPatterns {
		t.Errorf("cache holds %d patterns, want at most %d", size, maxCachedPatterns)
	}
}
