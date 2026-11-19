package score

import (
	"regexp"
	"sync"
)

// patternCache keeps each regex rule compiled once: a search judges every release against every rule, and compiling
// cost more than matching. Nil records a pattern that does not compile.
var patternCache = struct {
	sync.Mutex
	compiled map[string]*regexp.Regexp
	compiles int
}{compiled: map[string]*regexp.Regexp{}}

// maxCachedPatterns bounds the cache; rules are few, and one past it starts the cache over.
const maxCachedPatterns = 1024

// CompiledPattern returns the pattern compiled once, keyed by its full text, or nil when it does not compile.
func CompiledPattern(pattern string) *regexp.Regexp {
	patternCache.Lock()
	defer patternCache.Unlock()
	if compiled, found := patternCache.compiled[pattern]; found {
		return compiled
	}
	compiled, err := regexp.Compile(pattern)
	if err != nil {
		compiled = nil
	}
	patternCache.compiles++
	if len(patternCache.compiled) >= maxCachedPatterns {
		clear(patternCache.compiled)
	}
	patternCache.compiled[pattern] = compiled
	return compiled
}
