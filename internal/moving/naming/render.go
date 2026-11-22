package naming

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// maxStemBytes is what a file name may keep of the 255-byte component limit once its extension is counted: 24 bytes stay
// free for the longest suffix written behind it (".en.forced.srt", "-thumb.jpg", the linker's ".solarr-link-tmp" is a prefix).
const maxStemBytes = 255 - 24

// placeholderComponent stands in for a folder whose name cleaned to nothing, so the folder level is not lost.
const placeholderComponent = "_"

// Options is Lidarr's character handling, applied to every rendered path component --
// template literals included, so a literal ":" is replaced like one inside a title.
type Options struct {
	ReplaceIllegalCharacters bool
	ColonReplacement         ColonReplacement
}

// DefaultOptions is Lidarr's own default: replace, with smart colon replacement.
var DefaultOptions = Options{ReplaceIllegalCharacters: true, ColonReplacement: ColonSmart}

// ColonReplacement is Lidarr's colon replacement format.
type ColonReplacement string

// The colon replacement formats, Lidarr's five.
const (
	ColonDelete         ColonReplacement = "delete"
	ColonDash           ColonReplacement = "dash"
	ColonSpaceDash      ColonReplacement = "space_dash"
	ColonSpaceDashSpace ColonReplacement = "space_dash_space"
	// ColonSmart turns "Live: 1986" into "Live - 1986" and "1:05" into "1-05".
	ColonSmart ColonReplacement = "smart"
)

// ColonReplacements is every format, in the order the interface lists them.
var ColonReplacements = []ColonReplacement{ColonDelete, ColonDash, ColonSpaceDash, ColonSpaceDashSpace, ColonSmart}

// Valid reports whether c is one of ColonReplacements.
func (c ColonReplacement) Valid() bool {
	return slices.Contains(ColonReplacements, c)
}

// Context supplies a token's rendered value by name. A name absent from the
// map, or mapped to "", both mean "not available" -- Render does not
// distinguish "never had one" from "not populated yet".
type Context map[string]string

// Warning is a non-fatal oddity Render noticed -- a zero-padding spec
// applied to a value that is not a number, for instance. Rendering still
// produces a path; the caller decides whether to surface the warning.
type Warning struct {
	Token   string
	Message string
}

// Render turns one compiled template line into text; a folder line's literal "/" stays path structure (RenderPath splits it), and separator replaces only a "/" inside a resolved token value.
func Render(segments []Segment, ctx Context, opts Options) (string, []Warning) {
	var warnings []Warning
	return renderSegments(segments, ctx, opts, &warnings), warnings
}

func renderSegments(segments []Segment, ctx Context, opts Options, warnings *[]Warning) string {
	var b strings.Builder
	for _, seg := range segments {
		b.WriteString(renderOne(seg, ctx, opts, warnings))
	}
	return b.String()
}

func renderOne(seg Segment, ctx Context, opts Options, warnings *[]Warning) string {
	switch seg.kind {
	case segLiteral:
		return seg.literal
	case segToken:
		value, resolved := resolveToken(seg, ctx, warnings)
		if !resolved {
			return ""
		}
		return substituteSlashes(spell(value, seg), opts)
	case segConditional:
		if !allTokensResolve(seg.children, ctx) {
			return ""
		}
		return renderSegments(seg.children, ctx, opts, warnings)
	default:
		return ""
	}
}

// resolveToken tries each name in the fallback chain in order and formats
// the first non-empty value it finds.
func resolveToken(seg Segment, ctx Context, warnings *[]Warning) (string, bool) {
	for _, name := range seg.names {
		if value := lookup(ctx, name); value != "" {
			return formatValue(name, value, seg, warnings), true
		}
	}
	return "", false
}

// lookup is case-insensitive, so {ARTIST NAME} reads the "Artist Name" value.
func lookup(ctx Context, name string) string {
	if value, ok := ctx[name]; ok {
		return value
	}
	for key, value := range ctx {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// spell applies a token's spelling variant: {Artist.Name} writes "Vessa.Lund", {ARTIST NAME} upper case.
func spell(value string, seg Segment) string {
	if seg.wordSeparator != 0 {
		value = strings.ReplaceAll(value, " ", string(seg.wordSeparator))
	}
	switch seg.letterCase {
	case caseUpper:
		return strings.ToUpper(value)
	case caseLower:
		return strings.ToLower(value)
	default:
		return value
	}
}

func formatValue(name, value string, seg Segment, warnings *[]Warning) string {
	if seg.padWidth > 0 {
		if n, err := strconv.Atoi(value); err == nil {
			return fmt.Sprintf("%0*d", seg.padWidth, n)
		}
		if padded, ok := padLeadingNumber(value, seg.padWidth); ok {
			return padded
		}
		*warnings = append(*warnings, Warning{
			Token: name, Message: fmt.Sprintf("zero-padding requested but %q is not a number", value),
		})
		return value
	}
	if seg.maxChars > 0 && utf8.RuneCountInString(value) > seg.maxChars {
		runes := []rune(value)
		return string(runes[:seg.maxChars])
	}
	return value
}

// padLeadingNumber pads the digits a value starts with and keeps the rest: "1.5" and "1AU" pad to "001.5" and "001AU".
func padLeadingNumber(value string, width int) (string, bool) {
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == 0 || end == len(value) {
		return "", false
	}
	n, err := strconv.Atoi(value[:end])
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%0*d", width, n) + value[end:], true
}

// allTokensResolve reports whether every token directly inside segments
// (not recursing into a nested conditional, which is not supported) has a
// value -- what gates whether a conditional block is emitted at all.
func allTokensResolve(segments []Segment, ctx Context) bool {
	for _, seg := range segments {
		if seg.kind == segToken && !tokenResolves(seg, ctx) {
			return false
		}
	}
	return true
}

// tokenResolves is resolveToken's existence check, without formatting --
// used only to decide whether a conditional block is emitted, so it need
// not format the value or record a padding warning twice.
func tokenResolves(seg Segment, ctx Context) bool {
	for _, name := range seg.names {
		if lookup(ctx, name) != "" {
			return true
		}
	}
	return false
}

// substituteSlashes keeps a value's own slash from becoming a folder boundary: "XT/QK" is
// "XT+QK" when replacing, "XTQK" when not -- Lidarr's pair.
func substituteSlashes(value string, opts Options) string {
	return strings.Map(func(r rune) rune {
		if r != '/' && r != '\\' {
			return r
		}
		if opts.ReplaceIllegalCharacters {
			return '+'
		}
		return -1
	}, value)
}

// MaxRecommendedRelPathBytes is the total-path counterpart of the per-component limit; RenderPath sees only the relative path, so this leaves headroom for the root within the 260-byte Windows MAX_PATH.
const MaxRecommendedRelPathBytes = 240

// RenderPath renders one template into a relative path: its literal "/" characters separate
// folders, and the last component is the file, extension appended verbatim. Every component is
// cleaned per opts and then fsx.SanitiseComponent. Refused only when nothing renderable survives.
func RenderPath(template []Segment, ctx Context, opts Options, extension string) (string, []Warning, error) {
	var warnings []Warning
	rendered := renderSegments(template, ctx, opts, &warnings)

	raw := strings.Split(rendered, "/")
	file := cleanComponent(raw[len(raw)-1], opts, maxStemBytes-len(extension))
	if file == "" {
		return "", warnings, fmt.Errorf("naming: the file name rendered to nothing for %v", ctx)
	}
	var components []string
	for _, folder := range raw[:len(raw)-1] {
		cleaned := cleanComponent(folder, opts, 0)
		if cleaned == "" && strings.TrimSpace(folder) != "" {
			cleaned = placeholderComponent
		}
		if cleaned != "" {
			components = append(components, cleaned)
		}
	}
	components = append(components, file+extension)

	path := strings.Join(components, "/")
	if len(path) > MaxRecommendedRelPathBytes {
		warnings = append(warnings, Warning{
			Message: fmt.Sprintf(
				"rendered path is %d bytes, over the %d-byte recommended limit -- combined with a root "+
					"folder path, this may exceed common filesystem/SMB limits on some platforms",
				len(path), MaxRecommendedRelPathBytes),
		})
	}
	return path, warnings, nil
}

// illegalReplacements is Lidarr's table; with ReplaceIllegalCharacters off every key is removed.
var illegalReplacements = map[rune]string{
	'\\': "+", '/': "+", '<': "", '>': "", '?': "!", '*': "-", '|': "", '"': "",
}

// trailingSeparators is Lidarr's cleanup for an empty last token: "Title - " is not a name.
var trailingSeparators = regexp.MustCompile(`[- ._]+$`)

// emptyBrackets is what "({Release Year})" leaves behind when the year is unknown.
var emptyBrackets = regexp.MustCompile(`\s*(?:\(\s*\)|\[\s*\])`)

// cleanComponent normalises and cleans one component. A budget above zero keeps the result within that many bytes by
// eliding the middle: the end of a name is where two files differ.
func cleanComponent(raw string, opts Options, budget int) string {
	raw = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, norm.NFC.String(raw))
	cleaned := emptyBrackets.ReplaceAllString(replaceColons(raw, opts), "")
	var b strings.Builder
	for _, r := range cleaned {
		replacement, illegal := illegalReplacements[r]
		switch {
		case !illegal:
			b.WriteRune(r)
		case opts.ReplaceIllegalCharacters:
			b.WriteString(replacement)
		}
	}
	cleaned = collapseRepeats(b.String())
	cleaned = trailingSeparators.ReplaceAllString(strings.TrimSpace(cleaned), "")
	// A leading dot makes a hidden folder that players and file managers skip.
	cleaned = strings.TrimLeft(cleaned, ". ")
	if cleaned == "" {
		return ""
	}
	if budget > 0 {
		cleaned = elideMiddle(cleaned, budget)
	}
	return fsx.SanitiseComponent(cleaned)
}

// elideMiddle shortens s to at most limit bytes, keeping its head and its tail around an ellipsis, on rune boundaries.
func elideMiddle(s string, limit int) string {
	const ellipsis = "…"
	if len(s) <= limit {
		return s
	}
	room := limit - len(ellipsis)
	head, tail := room*2/3, room-room*2/3
	cutHead := head
	for cutHead > 0 && !utf8.RuneStart(s[cutHead]) {
		cutHead--
	}
	cutTail := len(s) - tail
	for cutTail < len(s) && !utf8.RuneStart(s[cutTail]) {
		cutTail++
	}
	return s[:cutHead] + ellipsis + s[cutTail:]
}

// collapseRepeats is Lidarr's `([- ._])(\1)+`: a doubled separator becomes one. Go's regexp
// has no backreference, so it is done by hand.
func collapseRepeats(s string) string {
	runes := []rune(s)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		b.WriteRune(runes[i])
		if !strings.ContainsRune("- ._", runes[i]) {
			continue
		}
		for i+1 < len(runes) && runes[i+1] == runes[i] {
			i++
		}
	}
	return b.String()
}

func replaceColons(s string, opts Options) string {
	if !strings.ContainsRune(s, ':') {
		return s
	}
	if !opts.ReplaceIllegalCharacters {
		return strings.ReplaceAll(s, ":", "")
	}
	switch opts.ColonReplacement {
	case ColonDash:
		return strings.ReplaceAll(s, ":", "-")
	case ColonSpaceDash:
		return strings.ReplaceAll(s, ":", " -")
	case ColonSpaceDashSpace:
		return strings.ReplaceAll(s, ":", " - ")
	case ColonSmart:
		return strings.ReplaceAll(strings.ReplaceAll(s, ": ", " - "), ":", "-")
	default:
		return strings.ReplaceAll(s, ":", "")
	}
}
