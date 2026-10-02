package naming

import (
	"fmt"
	"strconv"
	"strings"
)

type segmentKind int

const (
	segLiteral segmentKind = iota
	segToken
	segConditional
)

// Segment is one piece of a compiled template: literal text, a token
// (possibly a fallback chain, with an optional zero-padding or truncation
// spec), or a conditional block of child segments emitted only when every
// token directly inside it resolves to a non-empty value (section 11.8's
// "[ - {Edition}]", emitted only when Edition is non-empty).
type Segment struct {
	kind     segmentKind
	literal  string
	names    []string  // fallback chain, in declared order; len==1 for a plain token
	padWidth int       // 0 means no zero-padding requested
	maxChars int       // 0 means no truncation requested
	children []Segment // only set for segConditional
	// wordSeparator and letterCase are Lidarr's spelling variants: {Artist.Name}, {ARTIST NAME}.
	wordSeparator rune
	letterCase    letterCase
}

type letterCase int

const (
	caseAsIs letterCase = iota
	caseUpper
	caseLower
)

// tokenWordSeparators are the characters Lidarr accepts in place of a token name's spaces.
const tokenWordSeparators = "._-"

// Compile parses one template line (a folder or file spec) into segments.
// An unknown escape, an unclosed brace or bracket, or an unrecognised spec
// is a compile error here -- never a runtime "{Unknown Token}" in a
// filename (section 11.8). Compile does not know what token names are
// valid: that vocabulary is declared by the caller (ADR-23), checked
// separately by ValidateTokens.
func Compile(spec string) ([]Segment, error) {
	return parse([]rune(spec), false)
}

// ValidateTokens reports every token name in segments (including inside
// conditional blocks and fallback chains) that known does not recognise --
// the "unknown token is a compile-time error" half of section 11.8, kept
// separate from Compile because the vocabulary is the caller's, not this
// package's.
func ValidateTokens(segments []Segment, known map[string]bool) error {
	var unknown []string
	var walk func([]Segment)
	walk = func(segments []Segment) {
		for _, seg := range segments {
			switch seg.kind {
			case segToken:
				for _, name := range seg.names {
					if !knownFold(known, name) {
						unknown = append(unknown, name)
					}
				}
			case segConditional:
				walk(seg.children)
			}
		}
	}
	walk(segments)
	if len(unknown) > 0 {
		return fmt.Errorf("naming: unknown token(s): %s", strings.Join(unknown, ", "))
	}
	return nil
}

// knownFold is a case-insensitive lookup: {ARTIST NAME} names the same token as {Artist Name}.
func knownFold(known map[string]bool, name string) bool {
	if known[name] {
		return true
	}
	for candidate := range known {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

func parse(runes []rune, insideConditional bool) ([]Segment, error) {
	var segments []Segment
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			segments = append(segments, Segment{kind: segLiteral, literal: literal.String()})
			literal.Reset()
		}
	}

	for i := 0; i < len(runes); {
		switch runes[i] {
		case '{':
			if i+1 < len(runes) && runes[i+1] == '{' {
				literal.WriteRune('{')
				i += 2
				continue
			}
			end := indexRune(runes, i+1, '}')
			if end < 0 {
				return nil, fmt.Errorf("naming: unclosed '{' at position %d", i)
			}
			flush()
			seg, err := parseToken(string(runes[i+1 : end]))
			if err != nil {
				return nil, err
			}
			segments = append(segments, seg)
			i = end + 1
		case '}':
			if i+1 < len(runes) && runes[i+1] == '}' {
				literal.WriteRune('}')
				i += 2
				continue
			}
			return nil, fmt.Errorf("naming: unmatched '}' at position %d", i)
		case '[':
			if !insideConditional && i+1 < len(runes) && runes[i+1] == '[' {
				literal.WriteRune('[')
				i += 2
				continue
			}
			if insideConditional {
				return nil, fmt.Errorf("naming: nested '[' at position %d is not supported", i)
			}
			end := indexRune(runes, i+1, ']')
			if end < 0 {
				return nil, fmt.Errorf("naming: unclosed '[' at position %d", i)
			}
			flush()
			children, err := parse(runes[i+1:end], true)
			if err != nil {
				return nil, err
			}
			segments = append(segments, Segment{kind: segConditional, children: children})
			i = end + 1
		case ']':
			if !insideConditional && i+1 < len(runes) && runes[i+1] == ']' {
				literal.WriteRune(']')
				i += 2
				continue
			}
			if !insideConditional {
				return nil, fmt.Errorf("naming: unmatched ']' at position %d", i)
			}
			return nil, fmt.Errorf("naming: unexpected ']' at position %d", i)
		default:
			literal.WriteRune(runes[i])
			i++
		}
	}
	flush()
	return segments, nil
}

func indexRune(runes []rune, start int, target rune) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}
	return -1
}

// parseToken parses one `{...}` body: a `|`-separated fallback chain of
// names, with an optional `:spec` suffix applying to whichever alternative
// resolves. "00" (all digits) is a zero-padding width; ".60" is a
// truncation length.
func parseToken(body string) (Segment, error) {
	namesPart, spec, hasSpec := strings.Cut(body, ":")

	var names []string
	var wordSeparator rune
	for _, name := range strings.Split(namesPart, "|") {
		name = strings.TrimSpace(name)
		if name == "" {
			return Segment{}, fmt.Errorf("naming: empty token name in %q", body)
		}
		if separator, ok := spelledWordSeparator(name); ok {
			wordSeparator = separator
			name = strings.ReplaceAll(name, string(separator), " ")
		}
		names = append(names, name)
	}
	seg := Segment{kind: segToken, names: names, wordSeparator: wordSeparator, letterCase: spelledCase(namesPart)}
	if !hasSpec || spec == "" {
		return seg, nil
	}

	if after, ok := strings.CutPrefix(spec, "."); ok {
		n, err := strconv.Atoi(after)
		if err != nil || n <= 0 {
			return Segment{}, fmt.Errorf("naming: invalid truncation spec %q in token %q", spec, body)
		}
		seg.maxChars = n
		return seg, nil
	}
	if isAllDigits(spec) {
		seg.padWidth = len(spec)
		return seg, nil
	}
	return Segment{}, fmt.Errorf("naming: unrecognised spec %q in token %q", spec, body)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// spelledWordSeparator finds the one separator a name uses instead of spaces, as in {Artist.Name}.
func spelledWordSeparator(name string) (rune, bool) {
	if strings.ContainsRune(name, ' ') {
		return 0, false
	}
	for _, separator := range tokenWordSeparators {
		if strings.ContainsRune(name, separator) {
			return separator, true
		}
	}
	return 0, false
}

// spelledCase is upper for {ARTIST NAME} and lower for {artist name}; a one-word name
// such as {track} is Lidarr's canonical spelling, never a request for lower case.
func spelledCase(name string) letterCase {
	if strings.ToUpper(name) == strings.ToLower(name) {
		return caseAsIs
	}
	if name == strings.ToUpper(name) {
		return caseUpper
	}
	if name == strings.ToLower(name) && strings.ContainsAny(name, " "+tokenWordSeparators) {
		return caseLower
	}
	return caseAsIs
}
