// Package spec runs indexers described as data: an address, how to sign in, how to search and how to read a result row.
// The specs are converted from github.com/Prowlarr/Indexers by scripts/gen-indexer-specs.py; this package is our own engine.
package spec

import (
	"bytes"
	"compress/gzip"
	_ "embed" // the converted specs
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sync"
)

//go:embed data/specs.json.gz
var specsGz []byte

// Spec is one indexer: where it is, who may use it, how to sign in, search and read its answers.
type Spec struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	Language       string    `json:"language"`
	Privacy        string    `json:"privacy"`
	Encoding       string    `json:"encoding"`
	Links          []string  `json:"links"`
	LegacyLinks    []string  `json:"legacy_links"`
	RequestDelay   float64   `json:"request_delay"`
	FollowRedirect bool      `json:"follow_redirect"`
	Settings       []Setting `json:"settings"`
	Caps           Caps      `json:"caps"`
	Login          *Login    `json:"login"`
	Search         Search    `json:"search"`
	Download       *Download `json:"download"`
}

// Setting is one thing the owner fills in when adding the indexer.
type Setting struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Label   string   `json:"label"`
	Default string   `json:"default"`
	Options []Option `json:"options"`
}

// Option is one choice of a select setting.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Caps are the categories the site files releases under and the search modes it takes.
type Caps struct {
	Categories []CategoryMapping   `json:"categories"`
	Modes      map[string][]string `json:"modes"`
}

// CategoryMapping maps one of the site's categories to a Newznab one by name ("Movies/HD").
type CategoryMapping struct {
	ID          string `json:"id"`
	Cat         string `json:"cat"`
	Description string `json:"desc"`
	Default     string `json:"default"`
}

// Login is how the site is signed in to; Method is "post", "form", "get", "cookie" or "oneurl".
type Login struct {
	Method            string               `json:"method"`
	Path              string               `json:"path"`
	SubmitPath        string               `json:"submitpath"`
	Form              string               `json:"form"`
	Inputs            map[string]string    `json:"inputs"`
	SelectorInputs    map[string]Extractor `json:"selectorinputs"`
	GetSelectorInputs map[string]Extractor `json:"getselectorinputs"`
	Cookies           []string             `json:"cookies"`
	Error             []ErrorSelector      `json:"error"`
	Test              *LoginTest           `json:"test"`
	Captcha           *Captcha             `json:"captcha"`
	Headers           map[string][]string  `json:"headers"`
}

// LoginTest is a page that only a signed-in visitor sees.
type LoginTest struct {
	Path     string `json:"path"`
	Selector string `json:"selector"`
}

// Captcha is a challenge the sign-in asks; Solarr cannot answer one.
type Captcha struct {
	Type string `json:"type"`
}

// ErrorSelector finds an error message on a page: when Selector matches, the page is a failure.
type ErrorSelector struct {
	Selector string     `json:"selector"`
	Message  *Extractor `json:"message"`
}

// Search is how a query becomes requests and an answer becomes releases.
type Search struct {
	Paths                []SearchPath        `json:"paths"`
	Inputs               map[string]string   `json:"inputs"`
	Headers              map[string][]string `json:"headers"`
	KeywordsFilters      []Filter            `json:"keywords_filters"`
	PreprocessingFilters []Filter            `json:"preprocessing_filters"`
	AllowEmptyInputs     bool                `json:"allow_empty_inputs"`
	Error                []ErrorSelector     `json:"error"`
	Rows                 Rows                `json:"rows"`
	Fields               []Extractor         `json:"fields"`
}

// SearchPath is one request of a search.
type SearchPath struct {
	Path           string            `json:"path"`
	Method         string            `json:"method"`
	Inputs         map[string]string `json:"inputs"`
	Categories     []string          `json:"categories"`
	Response       map[string]string `json:"response"`
	FollowRedirect bool              `json:"follow_redirect"`
}

// Rows finds the releases in an answer.
type Rows struct {
	Selector                        string          `json:"selector"`
	Attribute                       string          `json:"attribute"`
	After                           string          `json:"after"`
	Remove                          string          `json:"remove"`
	Filters                         []Filter        `json:"filters"`
	Count                           json.RawMessage `json:"count"`
	DateHeaders                     json.RawMessage `json:"date_headers"`
	Multiple                        bool            `json:"multiple"`
	MissingAttributeEqualsNoResults bool            `json:"missing_attribute_equals_no_results"`
}

// Extractor reads one value: by selector, attribute and filters, or as a template (Text), or by the first matching case.
type Extractor struct {
	Name      string   `json:"name"`
	Selector  string   `json:"selector"`
	Attribute string   `json:"attribute"`
	Text      *string  `json:"text"`
	Default   *string  `json:"default"`
	Remove    string   `json:"remove"`
	Optional  bool     `json:"optional"`
	Filters   []Filter `json:"filters"`
	Case      []Case   `json:"case"`
}

// Case is a value taken when its selector matches.
type Case struct {
	Selector string `json:"selector"`
	Value    string `json:"value"`
}

// Filter is a step applied to an extracted value.
type Filter struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
}

// Download is how a result's file is found when its link is not the file.
type Download struct {
	Method    string              `json:"method"`
	Before    *Before             `json:"before"`
	Infohash  *Infohash           `json:"infohash"`
	Headers   map[string][]string `json:"headers"`
	Selectors []Extractor         `json:"selectors"`
}

// Before is a request made before the file is fetched.
type Before struct {
	Path         string            `json:"path"`
	Method       string            `json:"method"`
	Inputs       map[string]string `json:"inputs"`
	PathSelector string            `json:"pathselector"`
}

// Infohash finds a torrent's hash on its details page.
type Infohash struct {
	Hash              *Extractor `json:"hash"`
	Title             *Extractor `json:"title"`
	UseBeforeResponse string     `json:"usebeforeresponse"`
}

type file struct {
	Source string `json:"source"`
	Commit string `json:"commit"`
	Specs  []Spec `json:"specs"`
}

var (
	loadOnce sync.Once
	loaded   file
	loadErr  error
)

func load() {
	reader, err := gzip.NewReader(bytes.NewReader(specsGz))
	if err != nil {
		loadErr = fmt.Errorf("spec: open specs: %w", err)
		return
	}
	defer func() { _ = reader.Close() }()
	raw, err := io.ReadAll(reader)
	if err != nil {
		loadErr = fmt.Errorf("spec: read specs: %w", err)
		return
	}
	if err := json.Unmarshal(raw, &loaded); err != nil {
		loadErr = fmt.Errorf("spec: parse specs: %w", err)
	}
}

// All answers every spec, sorted as the file is (by id). The slice is shared: do not change it.
func All() ([]Spec, error) {
	loadOnce.Do(load)
	return loaded.Specs, loadErr
}

// Get answers the spec with the id.
func Get(id string) (Spec, bool) {
	specs, err := All()
	if err != nil {
		return Spec{}, false
	}
	at := slices.IndexFunc(specs, func(one Spec) bool { return one.ID == id })
	if at < 0 {
		return Spec{}, false
	}
	return specs[at], true
}

// Commit is the upstream commit the specs were converted from.
func Commit() string {
	loadOnce.Do(load)
	return loaded.Commit
}
