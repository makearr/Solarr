package extract

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlclark/regexp2"
)

// The specs are written for .NET, so patterns run on its engine: lookaround, back-references and Unicode block names.
// Everything else in the package reaches the engine through compiled, replace and firstGroup.

// matchTimeout stops a backtracking pattern on a hostile page from pinning a CPU.
const matchTimeout = 200 * time.Millisecond

// maxCachedPatterns bounds the cache: a pattern that names the search keywords is a new pattern for every search.
const maxCachedPatterns = 2048

var (
	patterns     sync.Map
	patternCount atomic.Int64
)

// unicodeBlocks finds .NET's block names (\p{IsCyrillic}), which the engine knows as scripts (\p{Cyrillic}).
var unicodeBlocks = regexp.MustCompile(`\\[pP]\{Is[A-Za-z]+\}`)

// scriptName renames one block escape; the CJK block is the Han script.
func scriptName(escape string) string {
	name := strings.TrimSuffix(strings.Replace(escape, "{Is", "{", 1), "}")
	if strings.HasSuffix(name, "{CJKUnifiedIdeographs") {
		name = name[:len(name)-len("CJKUnifiedIdeographs")] + "Han"
	}
	return name + "}"
}

// Compiled answers the pattern's compiled form, compiling each pattern once.
func Compiled(pattern string) (*regexp2.Regexp, error) {
	if cached, ok := patterns.Load(pattern); ok {
		return cached.(*regexp2.Regexp), nil
	}
	re, err := regexp2.Compile(unicodeBlocks.ReplaceAllStringFunc(pattern, scriptName), regexp2.RegexOptions(0))
	if err != nil {
		return nil, fmt.Errorf("spec: pattern %q: %w", pattern, err)
	}
	re.MatchTimeout = matchTimeout
	if patternCount.Load() < maxCachedPatterns {
		if stored, loaded := patterns.LoadOrStore(pattern, re); loaded {
			return stored.(*regexp2.Regexp), nil
		}
		patternCount.Add(1)
	}
	return re, nil
}

// replace is .NET's Regex.Replace: $1 and ${name} in the replacement name groups. ok is false when the pattern ran out of time.
func replace(re *regexp2.Regexp, input, replacement string) (replaced string, ok bool) {
	replaced, err := re.Replace(input, replacement, -1, -1)
	return replaced, err == nil
}

// firstGroup answers the first non-empty capture group of the first match, the whole match when the pattern has no
// group, and "" when nothing matched or the pattern ran out of time.
func firstGroup(re *regexp2.Regexp, input string) string {
	match, err := re.FindStringMatch(input)
	if err != nil || match == nil {
		return ""
	}
	groups := match.Groups()
	if len(groups) == 1 {
		return match.String()
	}
	for _, group := range groups[1:] {
		if text := group.String(); text != "" {
			return text
		}
	}
	return ""
}
