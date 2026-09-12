package naming

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/makearr/MediaNexus/internal/platform/fsx"
)

// DefaultSeparator replaces a literal "/" found inside a rendered token's
// own value (an artist named "XT/QK"), so it cannot be mistaken for a path
// boundary the template itself did not declare. "+" is section 11.8's
// default, reversed from an earlier "_" decision on evidence: the user's
// real library already writes "XT+QK".
const DefaultSeparator = '+'

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

// Render turns one compiled template line into text -- a folder line's own
// literal "/" characters stay as path structure; RenderPath is what splits
// them into sanitised components. separator substitutes for a "/" that
// arrives inside a token's resolved value, never for one in the template's
// own literal text.
func Render(segments []Segment, ctx Context, separator rune) (string, []Warning) {
	var warnings []Warning
	return renderSegments(segments, ctx, separator, &warnings), warnings
}

func renderSegments(segments []Segment, ctx Context, separator rune, warnings *[]Warning) string {
	var b strings.Builder
	for _, seg := range segments {
		b.WriteString(renderOne(seg, ctx, separator, warnings))
	}
	return b.String()
}

func renderOne(seg Segment, ctx Context, separator rune, warnings *[]Warning) string {
	switch seg.kind {
	case segLiteral:
		return seg.literal
	case segToken:
		value, resolved := resolveToken(seg, ctx, warnings)
		if !resolved {
			return ""
		}
		return substituteSeparator(value, separator)
	case segConditional:
		if !allTokensResolve(seg.children, ctx) {
			return ""
		}
		return renderSegments(seg.children, ctx, separator, warnings)
	default:
		return ""
	}
}

// resolveToken tries each name in the fallback chain in order and formats
// the first non-empty value it finds.
func resolveToken(seg Segment, ctx Context, warnings *[]Warning) (string, bool) {
	for _, name := range seg.names {
		if value, ok := ctx[name]; ok && value != "" {
			return formatValue(name, value, seg, warnings), true
		}
	}
	return "", false
}

func formatValue(name, value string, seg Segment, warnings *[]Warning) string {
	if seg.padWidth > 0 {
		if n, err := strconv.Atoi(value); err == nil {
			return fmt.Sprintf("%0*d", seg.padWidth, n)
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
		if value, ok := ctx[name]; ok && value != "" {
			return true
		}
	}
	return false
}

func substituteSeparator(value string, separator rune) string {
	if separator == 0 {
		separator = DefaultSeparator
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' {
			return separator
		}
		return r
	}, value)
}

// MaxRecommendedRelPathBytes is the per-component limit's own total-path
// counterpart (section 11.8's checklist: "the per-component limit is
// enforced" already, the combined length was not). RenderPath sees only the
// relative path, never the root folder it will be joined under, so this
// leaves headroom for a root's own path length within Windows' conventional
// 260-byte MAX_PATH rather than checking the true final absolute path --
// the part the naming interpreter actually controls, not a guarantee about
// the placed file's own final path.
const MaxRecommendedRelPathBytes = 240

// RenderPath renders a folder line and a file line into one sanitised,
// slash-joined relative path -- the folder line's own literal "/"
// characters become real path components, each passed through
// fsx.SanitiseComponent; the file line renders as the final component, with
// extension appended verbatim (it is not a template token: preserving it
// through truncation is the point, section 11.8). Returns an error only
// when nothing renderable survived at all -- a template that resolves to
// pure noise is a configuration problem, not a warning.
func RenderPath(folder, file []Segment, ctx Context, separator rune, extension string) (string, []Warning, error) {
	var warnings []Warning
	folderText := renderSegments(folder, ctx, separator, &warnings)
	fileText := renderSegments(file, ctx, separator, &warnings)

	components := splitAndSanitise(folderText)
	fileComponent := sanitiseOne(fileText)
	if fileComponent == "" {
		return "", warnings, fmt.Errorf("naming: file template rendered to nothing for %v", ctx)
	}
	components = append(components, fileComponent+extension)

	if len(components) == 0 {
		return "", warnings, fmt.Errorf("naming: template rendered to an empty path for %v", ctx)
	}
	rendered := strings.Join(components, "/")
	if len(rendered) > MaxRecommendedRelPathBytes {
		warnings = append(warnings, Warning{
			Message: fmt.Sprintf(
				"rendered path is %d bytes, over the %d-byte recommended limit -- combined with a root "+
					"folder path, this may exceed common filesystem/SMB limits on some platforms",
				len(rendered), MaxRecommendedRelPathBytes),
		})
	}
	return rendered, warnings, nil
}

func splitAndSanitise(rendered string) []string {
	var components []string
	for _, raw := range strings.Split(rendered, "/") {
		if sanitised := sanitiseOne(raw); sanitised != "" {
			components = append(components, sanitised)
		}
	}
	return components
}

func sanitiseOne(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return fsx.SanitiseComponent(trimmed)
}
