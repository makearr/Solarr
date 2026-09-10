package config

import (
	"fmt"
	"sort"
	"strings"
)

// redacted replaces the value of any setting that could carry a secret.
const redacted = "<redacted>"

// secretPaths are settings whose values must never reach a log, an error or the
// API (Invariant 61).
var secretPaths = map[string]bool{
	"security.encryption_key": true,
}

// secretWords catch a key that is not in the schema at all.
var secretWords = []string{"key", "secret", "password", "passwd", "token", "credential"}

// isSecretPath reports whether a value at this path must be redacted.
func isSecretPath(path string) bool {
	if secretPaths[path] {
		return true
	}
	lowered := strings.ToLower(path)
	for _, word := range secretWords {
		if strings.Contains(lowered, word) {
			return true
		}
	}
	return false
}

// redact returns the value as it may be shown.
func redact(path, value string) string {
	if isSecretPath(path) {
		return redacted
	}
	return value
}

// redactedParseFailure replaces a parser's message when the value being parsed is a secret.
const redactedParseFailure = "could not be parsed (details withheld because the value is a secret)"

// sanitiseErrorText redacts any line of a parser error that mentions a secret setting, for
// errors that describe the document as a whole rather than one field.
func sanitiseErrorText(text string) string {
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lowered := strings.ToLower(line)
		for _, word := range secretWords {
			if strings.Contains(lowered, word) {
				lines[index] = "    " + redacted
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// Problem is one thing wrong with the configuration: where it is, what was found, and what was
// expected (§6).
type Problem struct {
	// Path is dotted, as written in the file: "server.port".
	Path string
	// Value is what was found, redacted when Path names a secret.
	Value string
	// Expectation says what would have been accepted.
	Expectation string
	// Suggestion is the nearest valid key, for an unknown one.
	Suggestion string
}

func (p Problem) String() string {
	line := fmt.Sprintf("%-24s %-18s %s", p.Path, p.Value, p.Expectation)
	if p.Suggestion != "" {
		line += fmt.Sprintf(" — did you mean `%s`?", p.Suggestion)
	}
	return strings.TrimRight(line, " ")
}

// newProblem redacts as it constructs, so no caller can forget to.
func newProblem(path, value, expectation string) Problem {
	return Problem{Path: path, Value: redact(path, value), Expectation: expectation}
}

// Problems is every fault found in one document (ADR-44, Invariant 60).
type Problems []Problem

// Error renders §14.1's report: every problem at once, with a closing line
// making it unambiguous that nothing started.
func (p Problems) Error() string {
	return p.Report("config.yml", "Nothing has been started.")
}

// Report renders the list under the caller's own heading and closing line.
// See documentation/configuration-export.md.
func (p Problems) Report(subject, closing string) string {
	if len(p) == 0 {
		return "config: no problems"
	}

	sorted := make(Problems, len(p))
	copy(sorted, p)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	var builder strings.Builder
	fmt.Fprintf(&builder, "%s: %s\n\n", subject, pluralise(len(sorted), "error"))
	for _, problem := range sorted {
		fmt.Fprintf(&builder, "  %s\n", problem)
	}
	if closing != "" {
		builder.WriteString("\n" + closing)
	}
	return builder.String()
}

// OrNil returns nil when there is nothing wrong, so callers can `if err != nil`.
func (p Problems) OrNil() error {
	if len(p) == 0 {
		return nil
	}
	return p
}

func pluralise(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// nearestKey returns the closest valid key to an unknown one, or "" when
// nothing is close enough to be a useful guess.
func nearestKey(unknown string, candidates []string) string {
	// A third of the length, so a genuine typo finds its key while an unrelated
	// word suggests nothing. A wrong guess is worse than none.
	budget := max(len(unknown)/3, 1)

	best, bestDistance := "", budget+1
	for _, candidate := range candidates {
		distance := editDistance(unknown, candidate)
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	if bestDistance > budget {
		return ""
	}
	return best
}

// Damerau-Levenshtein, not plain Levenshtein: a transposition is the commonest
// typo and costs two edits under the latter, losing the suggestion.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}

	distance := make([][]int, len(a)+1)
	for i := range distance {
		distance[i] = make([]int, len(b)+1)
		distance[i][0] = i
	}
	for j := range distance[0] {
		distance[0][j] = j
	}

	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			substitution := distance[i-1][j-1]
			if a[i-1] != b[j-1] {
				substitution++
			}
			distance[i][j] = min(substitution, min(distance[i-1][j]+1, distance[i][j-1]+1))

			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				distance[i][j] = min(distance[i][j], distance[i-2][j-2]+1)
			}
		}
	}
	return distance[len(a)][len(b)]
}
