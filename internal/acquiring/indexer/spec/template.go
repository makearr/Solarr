package spec

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Variables are what a template can name: the search, the owner's settings and the values read so far from a row.
type Variables struct {
	Keywords    string
	Query       map[string]string
	Categories  []string
	Config      map[string]any
	Result      map[string]string
	Today       struct{ Year int }
	DownloadURI map[string]string
	True        bool
	False       bool
}

func newVariables(now time.Time) *Variables {
	variables := &Variables{Query: map[string]string{}, Config: map[string]any{}, Result: map[string]string{}, True: true}
	variables.Today.Year = now.Year()
	return variables
}

var templates sync.Map

var templateFuncs = template.FuncMap{
	"join": strings.Join,
	"re_replace": func(input, pattern, replacement string) string {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return input
		}
		return compiled.ReplaceAllString(input, dotNetReplacement(replacement))
	},
}

// render fills a template with the variables; a name that is not there is empty, and a template that does not parse is
// returned as it is, which fails visibly further on.
func render(text string, variables *Variables) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	cached, ok := templates.Load(text)
	if !ok {
		parsed, err := parseTemplate(text)
		if err != nil {
			return "", fmt.Errorf("spec: template %q: %w", text, err)
		}
		cached, _ = templates.LoadOrStore(text, parsed)
	}
	var out bytes.Buffer
	if err := cached.(*template.Template).Execute(&out, variables); err != nil {
		return "", fmt.Errorf("spec: template %q: %w", text, err)
	}
	return strings.ReplaceAll(out.String(), "<no value>", ""), nil
}

// dotNetReplacement turns .NET's $1 and ${name} into Go's ${1} and ${name}.
var dollarGroup = regexp.MustCompile(`\$(\d+)`)

func dotNetReplacement(replacement string) string {
	return dollarGroup.ReplaceAllString(replacement, "${$1}")
}

var (
	action     = regexp.MustCompile(`(?s)\{\{.*?\}\}`)
	dashedName = regexp.MustCompile(`\.(Config|Result|Query)\.([A-Za-z0-9_]*-[A-Za-z0-9_-]*)`)
	badEscape  = regexp.MustCompile(`\\([^abfnrtv\\"'xuU0-7])`)
)

// parseTemplate reads a template written for .NET: names with a dash are looked up by key, and a regular expression's
// backslash in a string ("\s+") is kept as a backslash.
func parseTemplate(text string) (*template.Template, error) {
	fixed := action.ReplaceAllStringFunc(text, func(one string) string {
		one = dashedName.ReplaceAllString(one, `(index .$1 "$2")`)
		return badEscape.ReplaceAllString(one, `\\$1`)
	})
	return template.New("spec").Funcs(templateFuncs).Parse(fixed)
}
