package specprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/platform/extract"
)

// MaxRequests bounds the requests one operation may make.
const MaxRequests = 8

// Definition is the body of an engine: http metadata file; the envelope fields are read by providerdef.
type Definition struct {
	Key          string                `json:"-"`
	Name         string                `json:"-"`
	Description  string                `json:"description"`
	MediaTypes   []string              `json:"media_types"`
	LibraryKinds []string              `json:"library_kinds"`
	Adult        bool                  `json:"adult"`
	Role         string                `json:"role"`
	Hosts        []string              `json:"hosts"`
	Pace         Pace                  `json:"pace"`
	Via          string                `json:"via"`
	Settings     []Setting             `json:"settings"`
	Headers      map[string]string     `json:"headers"`
	Cookies      map[string]string     `json:"cookies"`
	Cache        map[string]string     `json:"cache"`
	IDs          IDSpec                `json:"ids"`
	Search       *Operation            `json:"search"`
	Item         *Operation            `json:"item"`
	ByID         map[string]*Operation `json:"by_id"`
}

// Pace is a definition's request pace; the pace is mandatory.
type Pace struct {
	PerMinute    int64 `json:"per_minute"`
	MaxPerMinute int64 `json:"max_per_minute"`
}

// Setting is a value the owner fills in; password values are sealed and never written to the file.
type Setting struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

// IDSpec names the id namespaces a definition reads and writes.
type IDSpec struct {
	Own  string   `json:"own"`
	Also []string `json:"also"`
}

// Operation is one thing a provider does: search, read one item, look one up by another provider's id.
type Operation struct {
	Steps         []Step           `json:"steps"`
	Request       *Request         `json:"request"`
	Response      string           `json:"response"`
	Rows          *Rows            `json:"rows"`
	Fields        map[string]Field `json:"fields"`
	Parts         *Section         `json:"parts"`
	People        *Section         `json:"people"`
	Images        *Section         `json:"images"`
	Organisations *Section         `json:"organisations"`
}

// Step is an earlier request of an operation whose fields later requests name as {{ .Step.<name>.<field> }}.
type Step struct {
	Name     string           `json:"name"`
	Request  Request          `json:"request"`
	Response string           `json:"response"`
	Fields   map[string]Field `json:"fields"`
}

// Request is one HTTP request; Path is the whole address, a template.
type Request struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	Query    map[string]string `json:"query"`
	Body     string            `json:"body"`
	BodyType string            `json:"body_type"`
	Headers  map[string]string `json:"headers"`
}

// Rows selects the repeated elements of an answer.
type Rows struct {
	Selector string `json:"selector"`
}

// Section reads a list that belongs to the item: its parts, credits or pictures.
type Section struct {
	Rows   Rows             `json:"rows"`
	Fields map[string]Field `json:"fields"`
}

// Field reads one value: the extractor's selector, attribute, text, default, filters and cases, then Map into a closed
// vocabulary (an unmapped value is dropped and counted) or Split into a list.
type Field struct {
	Selector  string            `json:"selector"`
	Attribute string            `json:"attribute"`
	Remove    string            `json:"remove"`
	Text      *string           `json:"text"`
	Default   *string           `json:"default"`
	Optional  bool              `json:"optional"`
	Filters   []extract.Filter  `json:"filters"`
	Case      []extract.Case    `json:"case"`
	Map       map[string]string `json:"map"`
	Split     string            `json:"split"`
}

func (f Field) extractor(name string) extract.Extractor {
	return extract.Extractor{Name: name, Selector: f.Selector, Attribute: f.Attribute, Text: f.Text, Default: f.Default, Remove: f.Remove,
		Optional: f.Optional, Filters: f.Filters, Case: f.Case}
}

var (
	recordFields = []string{"id", "title", "original_title", "sort_title", "alt_titles", "overview", "year", "date", "end_date", "status", "language",
		"country", "genres", "tags", "adult", "age_rating", "rating", "votes", "page_count", "runtime_seconds", "links"}
	partFields         = []string{"kind", "number", "title", "date", "overview", "id"}
	personFields       = []string{"name", "role", "id"}
	imageFields        = []string{"url", "kind", "language", "width", "height"}
	organisationFields = []string{"name", "role"}
)

// Parse reads the definition file's body strictly: an unknown field, a field name that is not a Record field, a request
// to an address outside the declared hosts, or an operation with more than MaxRequests requests is an error.
func Parse(key, name string, data []byte) (Definition, error) {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return Definition{}, fmt.Errorf("specprovider: read yaml: %w", err)
	}
	for _, envelope := range []string{"schema", "kind", "key", "name", "engine", "enabled"} {
		delete(fields, envelope)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return Definition{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var def Definition
	if err := decoder.Decode(&def); err != nil {
		return Definition{}, fmt.Errorf("specprovider: %s: %w", key, err)
	}
	def.Key, def.Name = key, name
	if err := def.validate(); err != nil {
		return Definition{}, fmt.Errorf("specprovider: %s: %w", key, err)
	}
	return def, nil
}

func (d Definition) validate() error {
	if d.Pace.PerMinute <= 0 {
		return fmt.Errorf("pace.per_minute is required")
	}
	if len(d.Hosts) == 0 {
		return fmt.Errorf("hosts is required")
	}
	if d.Search == nil && d.Item == nil && len(d.ByID) == 0 {
		return fmt.Errorf("a definition needs search, item or by_id")
	}
	check := func(label string, op *Operation) error {
		if op == nil {
			return nil
		}
		return op.validate(label, d.Hosts)
	}
	if err := check("search", d.Search); err != nil {
		return err
	}
	if err := check("item", d.Item); err != nil {
		return err
	}
	for namespace, op := range d.ByID {
		if err := check("by_id."+namespace, op); err != nil {
			return err
		}
	}
	return nil
}

func (o *Operation) validate(label string, hosts []string) error {
	requests := len(o.Steps)
	if o.Request != nil {
		requests++
	}
	if requests == 0 || requests > MaxRequests {
		return fmt.Errorf("%s: %d requests, want 1 to %d", label, requests, MaxRequests)
	}
	all := o.Steps
	if o.Request != nil {
		all = append(slices.Clone(o.Steps), Step{Name: "request", Request: *o.Request, Response: o.Response})
	}
	for _, step := range all {
		if err := step.Request.checkAddress(hosts); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
		if !slices.Contains([]string{"", "json", "html", "xml"}, step.Response) {
			return fmt.Errorf("%s: response %q is not json, html or xml", label, step.Response)
		}
		if err := checkNames(label+" "+step.Name, step.Fields, nil); err != nil {
			return err
		}
	}
	if err := checkNames(label, o.Fields, recordFields); err != nil {
		return err
	}
	for name, section := range map[string]struct {
		section *Section
		allowed []string
	}{"parts": {o.Parts, partFields}, "people": {o.People, personFields}, "images": {o.Images, imageFields}, "organisations": {o.Organisations, organisationFields}} {
		if section.section != nil {
			if err := checkNames(label+" "+name, section.section.Fields, section.allowed); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkAddress refuses a request whose literal host is not declared; a template's host cannot be known until it runs, and
// is refused then.
func (r Request) checkAddress(hosts []string) error {
	if !strings.HasPrefix(r.Path, "https://") && !strings.HasPrefix(r.Path, "http://") {
		return fmt.Errorf("path %q is not an absolute address", r.Path)
	}
	authority := strings.SplitN(r.Path, "/", 4)[2]
	if strings.Contains(authority, "{{") {
		return nil
	}
	if host, _, _ := strings.Cut(authority, ":"); !slices.Contains(hosts, strings.ToLower(host)) {
		return fmt.Errorf("host %q is not declared", host)
	}
	return nil
}

func checkNames(label string, fields map[string]Field, allowed []string) error {
	if allowed == nil {
		return nil
	}
	for name := range fields {
		base, _, _ := strings.Cut(name, ".")
		if !slices.Contains(allowed, base) {
			return fmt.Errorf("%s: %q is not a field of the record", label, name)
		}
	}
	return nil
}
