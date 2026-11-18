package music

import (
	"strings"
	"unicode"
)

// neutralWords are qualifier words that say nothing about which recording a title is: a reissue, a spelling of the
// version, a content flag. "Live", "acoustic", "demo", "remix", "instrumental" are absent on purpose.
var neutralWords = map[string]bool{
	"remaster": true, "remastered": true, "remastering": true, "version": true, "album": true, "single": true,
	"original": true, "explicit": true, "clean": true, "bonus": true, "track": true, "mono": true, "stereo": true,
	"digital": true, "deluxe": true, "edition": true,
}

// creditWords open a credit; everything after one inside the same qualifier is the credited name.
var creditWords = map[string]bool{"feat": true, "ft": true, "featuring": true, "with": true}

// neutralCore is a track title without the qualifiers that only describe its pressing: a bracketed group, or a
// trailing " - ..." segment, whose words are all neutral or a year, or that is a credit. Qualifiers that tell
// recordings apart stay, so "Song (Live) (2009 Remaster)" is "Song (Live)".
func neutralCore(title string) string {
	var kept strings.Builder
	var group strings.Builder
	var open rune
	depth := 0
	for _, r := range title {
		switch {
		case depth == 0 && (r == '(' || r == '[' || r == '{'):
			depth, open = 1, r
			group.Reset()
		case depth > 0 && (r == '(' || r == '[' || r == '{'):
			depth++
			group.WriteRune(r)
		case depth > 0 && (r == ')' || r == ']' || r == '}'):
			depth--
			if depth > 0 {
				group.WriteRune(r)
				continue
			}
			if !neutralQualifier(group.String()) {
				kept.WriteRune(open)
				kept.WriteString(group.String())
				kept.WriteRune(r)
			}
		case depth > 0:
			group.WriteRune(r)
		default:
			kept.WriteRune(r)
		}
	}
	if depth > 0 { // an unclosed bracket is text, not a qualifier
		kept.WriteRune(open)
		kept.WriteString(group.String())
	}
	core := strings.Join(strings.Fields(kept.String()), " ")
	if index := strings.LastIndex(core, " - "); index > 0 && neutralQualifier(core[index+3:]) {
		core = core[:index]
	}
	return strings.TrimSpace(core)
}

// neutralQualifier reports a qualifier made only of neutral words and years, or opening with a credit.
func neutralQualifier(text string) bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		switch {
		case creditWords[word]:
			return true
		case neutralWords[word], isYear(word):
		default:
			return false
		}
	}
	return true
}

func isYear(word string) bool {
	if len(word) != 4 || (!strings.HasPrefix(word, "19") && !strings.HasPrefix(word, "20")) {
		return false
	}
	for _, r := range word {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
