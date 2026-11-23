// Package specclient runs a download client described in a definition file (engine: http): it says how to talk to an
// external client, Solarr implements none. The result stands behind download.UsenetClient like a compiled client.
package specclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/extract"
)

// MaxSteps bounds the requests of one probe or action.
const MaxSteps = 4

// Definition is one download client described as data. Paths are relative to the address the owner enters.
type Definition struct {
	Key                string     `json:"-"`
	Name               string     `json:"-"`
	Label              string     `json:"label"`
	AddressPlaceholder string     `json:"address_placeholder"`
	SecretLabel        string     `json:"secret_label"`
	UserName           bool       `json:"user_name"`
	KeepsCompleted     bool       `json:"keeps_completed"`
	Categories         bool       `json:"categories"`
	Auth               Auth       `json:"auth"`
	Probe              *Probe     `json:"probe"`
	Add                *Add       `json:"add"`
	Downloads          *Downloads `json:"downloads"`
	QueueStatus        *Queue     `json:"queue_status"`
	Remove             *Action    `json:"remove"`
	Pause              *Action    `json:"pause"`
	Resume             *Action    `json:"resume"`
}

// Auth is how the owner's secret travels: as a query value, a header, or HTTP basic ("user:password" in the secret).
type Auth struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Prefix string `json:"prefix"`
}

// Request is one call; an empty rendered query value is left out of the address.
type Request struct {
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	Query     map[string]string `json:"query"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	BodyType  string            `json:"body_type"`
	FileField string            `json:"file_field"`
	FileName  string            `json:"file_name"`
}

// Field reads one value of a row; BlankWhen empties it when the row matches the selector.
type Field struct {
	Selector  string           `json:"selector"`
	Attribute string           `json:"attribute"`
	Text      *string          `json:"text"`
	Default   *string          `json:"default"`
	Optional  bool             `json:"optional"`
	Filters   []extract.Filter `json:"filters"`
	Case      []extract.Case   `json:"case"`
	BlankWhen string           `json:"blank_when"`
}

func (f Field) extractor(name string) extract.Extractor {
	return extract.Extractor{Name: name, Selector: f.Selector, Attribute: f.Attribute, Text: f.Text, Default: f.Default,
		Optional: f.Optional, Filters: f.Filters, Case: f.Case}
}

// Step is one request of a probe or action and the values read from its answer.
type Step struct {
	Name    string           `json:"name"`
	Request Request          `json:"request"`
	Fields  map[string]Field `json:"fields"`
}

// Probe proves the address and the secret: version is required; download_folder and categories are optional.
type Probe struct {
	Steps  []Step           `json:"steps"`
	Fields map[string]Field `json:"fields"`
}

// Add uploads the release file; the answer names the client's reference (ref) and may say refused (ok false, error).
type Add struct {
	Request Request          `json:"request"`
	Fields  map[string]Field `json:"fields"`
}

// Downloads lists the client's downloads: one listing per place it keeps them (queue, history).
type Downloads struct {
	List  []Listing `json:"list"`
	ByRef []Listing `json:"by_ref"`
}

// Listing is one request whose rows are downloads; States maps the client's status text onto Solarr's states, and
// DefaultState (required) takes every status it does not name.
type Listing struct {
	Request        Request           `json:"request"`
	Rows           string            `json:"rows"`
	Fields         map[string]Field  `json:"fields"`
	States         map[string]string `json:"states"`
	DefaultState   string            `json:"default_state"`
	FilterCategory bool              `json:"filter_category"`
}

// Queue is the client's own totals: paused, speed (bytes a second), items, left (bytes).
type Queue struct {
	Request Request          `json:"request"`
	Fields  map[string]Field `json:"fields"`
}

// Action is one or more requests; a step fails when its error field reads non-empty.
type Action struct {
	Steps []Step `json:"steps"`
}

var (
	downloadFields = []string{"ref", "name", "status", "size", "left", "category", "path", "message"}
	queueFields    = []string{"paused", "speed", "items", "left"}
	addFields      = []string{"ref", "ok", "error"}
	probeFields    = []string{"version", "download_folder", "categories"}
	envelope       = []string{"schema", "kind", "key", "name", "engine", "enabled", "description", "protocol"}
)

// Parse reads one definition file; every error names the part at fault.
func Parse(key, name string, data []byte) (Definition, error) {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return Definition{}, fmt.Errorf("specclient: read yaml: %w", err)
	}
	if protocol, _ := fields["protocol"].(string); protocol != string(download.ProtocolUsenet) {
		return Definition{}, fmt.Errorf("specclient: %s: protocol %q is not offered (usenet only)", key, protocol)
	}
	for _, one := range envelope {
		delete(fields, one)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return Definition{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var def Definition
	if err := decoder.Decode(&def); err != nil {
		return Definition{}, fmt.Errorf("specclient: %s: %w", key, err)
	}
	def.Key, def.Name = key, name
	if def.Label == "" {
		def.Label = name
	}
	if err := def.validate(); err != nil {
		return Definition{}, fmt.Errorf("specclient: %s: %w", key, err)
	}
	return def, nil
}

// Describe is the settings screen's description of the kind.
func (d Definition) Describe() download.Definition {
	return download.Definition{
		Impl: d.Key, Label: d.Label, Protocol: download.ProtocolUsenet, AddressPlaceholder: d.AddressPlaceholder,
		UserName: d.UserName, SecretLabel: d.SecretLabel, KeepsCompleted: d.KeepsCompleted, Categories: d.Categories,
	}
}

func (d Definition) validate() error {
	if d.AddressPlaceholder == "" {
		return fmt.Errorf("address_placeholder is required")
	}
	switch d.Auth.Type {
	case "query", "header":
		if d.Auth.Name == "" {
			return fmt.Errorf("auth.name is required for %s", d.Auth.Type)
		}
	case "basic", "none", "":
	default:
		return fmt.Errorf("auth.type %q is not query, header, basic or none", d.Auth.Type)
	}
	if d.Probe == nil || d.Add == nil || d.Downloads == nil || d.QueueStatus == nil || d.Remove == nil || d.Pause == nil || d.Resume == nil {
		return fmt.Errorf("probe, add, downloads, queue_status, remove, pause and resume are all required")
	}
	if err := checkSteps("probe", d.Probe.Steps, 1); err != nil {
		return err
	}
	if err := checkNames("probe", d.Probe.Fields, probeFields, []string{"version"}); err != nil {
		return err
	}
	if err := d.Add.Request.check("add"); err != nil {
		return err
	}
	if d.Add.Request.FileField == "" {
		return fmt.Errorf("add.request.file_field is required: the release file is uploaded")
	}
	if err := checkNames("add", d.Add.Fields, addFields, []string{"ref"}); err != nil {
		return err
	}
	if len(d.Downloads.List) == 0 {
		return fmt.Errorf("downloads.list needs at least one listing")
	}
	for i, listing := range slices.Concat(d.Downloads.List, d.Downloads.ByRef) {
		if err := listing.validate(fmt.Sprintf("downloads listing %d", i+1)); err != nil {
			return err
		}
	}
	if err := d.QueueStatus.Request.check("queue_status"); err != nil {
		return err
	}
	if err := checkNames("queue_status", d.QueueStatus.Fields, queueFields, nil); err != nil {
		return err
	}
	for label, action := range map[string]*Action{"remove": d.Remove, "pause": d.Pause, "resume": d.Resume} {
		if err := checkSteps(label, action.Steps, 0); err != nil {
			return err
		}
	}
	return nil
}

func (l Listing) validate(label string) error {
	if err := l.Request.check(label); err != nil {
		return err
	}
	if err := checkNames(label, l.Fields, downloadFields, []string{"ref", "status"}); err != nil {
		return err
	}
	valid := func(state string) bool { return slices.Contains(allStates, download.State(state)) }
	if !valid(l.DefaultState) {
		return fmt.Errorf("%s: default_state %q is not a download state", label, l.DefaultState)
	}
	for status, state := range l.States {
		if !valid(state) {
			return fmt.Errorf("%s: states[%q] = %q is not a download state", label, status, state)
		}
	}
	return nil
}

var allStates = []download.State{download.StateQueued, download.StateDownloading, download.StatePaused, download.StateProcessing,
	download.StateCompleted, download.StateFailed, download.StateRemoved}

func checkSteps(label string, steps []Step, least int) error {
	if len(steps) < least || len(steps) > MaxSteps || (least == 0 && len(steps) == 0) {
		return fmt.Errorf("%s: %d requests, want 1 to %d", label, len(steps), MaxSteps)
	}
	for _, step := range steps {
		if step.Name == "" {
			return fmt.Errorf("%s: every step needs a name", label)
		}
		if err := step.Request.check(label + " " + step.Name); err != nil {
			return err
		}
		if err := checkNames(label+" "+step.Name, step.Fields, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// check refuses an address that could leave the owner's host, a method that is not a plain verb, and a template that does not parse.
func (r Request) check(label string) error {
	if !strings.HasPrefix(r.Path, "/") || strings.HasPrefix(r.Path, "//") || strings.Contains(r.Path, "://") || strings.Contains(r.Path, "@") {
		return fmt.Errorf("%s: path %q must start with a single / (it is relative to the owner's address)", label, r.Path)
	}
	if !slices.Contains([]string{"", "GET", "POST", "PUT", "DELETE"}, r.Method) {
		return fmt.Errorf("%s: method %q is not GET, POST, PUT or DELETE", label, r.Method)
	}
	texts := []string{r.Path, r.Body, r.FileName}
	for _, group := range []map[string]string{r.Query, r.Headers} {
		for _, value := range group {
			texts = append(texts, value)
		}
	}
	for _, text := range texts {
		if _, err := extract.ParseTemplate(text); err != nil {
			return fmt.Errorf("%s: template %q: %w", label, text, err)
		}
	}
	return nil
}

func checkNames(label string, fields map[string]Field, allowed, required []string) error {
	for name, field := range fields {
		if allowed != nil && !slices.Contains(allowed, name) {
			return fmt.Errorf("%s: field %q is not one of %s", label, name, strings.Join(allowed, ", "))
		}
		for _, filter := range field.Filters {
			if !extract.KnownFilters[filter.Name] {
				return fmt.Errorf("%s: field %q: unknown filter %q", label, name, filter.Name)
			}
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("%s: field %q is required", label, name)
		}
	}
	return nil
}
