package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/goccy/go-yaml"
)

// envelopeKeys are the definition file's own fields; a spec is everything else.
var envelopeKeys = []string{"schema", "kind", "key", "engine", "enabled"}

// YAML is the spec as a definition file: the envelope, then the spec's fields under their JSON names. Every string is
// written double-quoted, which the YAML decoder reads back exactly (a block scalar loses blank lines).
func (s Spec) YAML() ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("spec: encode %s: %w", s.ID, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out bytes.Buffer
	fmt.Fprintf(&out, "schema: 1\nkind: indexer\nkey: %s\nengine: spec\n", quoted(s.ID))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if name == "id" {
			var skip json.RawMessage
			if err := decoder.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		fmt.Fprintf(&out, "%s:", plainKey(name.(string)))
		if err := emitValue(&out, decoder, 0); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

// quoted is a YAML double-quoted scalar, which JSON's string syntax is.
func quoted(text string) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(buf.String(), "\n")
}

var plainKeyPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// plainKey leaves a snake_case key bare unless YAML would read it as another type.
func plainKey(key string) string {
	switch key {
	case "y", "n", "yes", "no", "true", "false", "null", "on", "off":
		return quoted(key)
	}
	if plainKeyPattern.MatchString(key) {
		return key
	}
	return quoted(key)
}

// emitValue writes the next JSON value from the decoder as block YAML after a key or a dash; indent is the column of that key.
func emitValue(out *bytes.Buffer, decoder *json.Decoder, indent int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch value := token.(type) {
	case json.Delim:
		return emitContainer(out, decoder, value, indent)
	case string:
		out.WriteString(" " + quoted(value) + "\n")
	case json.Number:
		out.WriteString(" " + value.String() + "\n")
	case bool:
		fmt.Fprintf(out, " %t\n", value)
	case nil:
		out.WriteString(" null\n")
	}
	return nil
}

func emitContainer(out *bytes.Buffer, decoder *json.Decoder, open json.Delim, indent int) error {
	pad := strings.Repeat(" ", indent+2)
	if !decoder.More() {
		_, err := decoder.Token()
		if open == '{' {
			out.WriteString(" {}\n")
		} else {
			out.WriteString(" []\n")
		}
		return err
	}
	out.WriteString("\n")
	for decoder.More() {
		if open == '{' {
			name, err := decoder.Token()
			if err != nil {
				return err
			}
			out.WriteString(pad + plainKey(name.(string)) + ":")
			if err := emitValue(out, decoder, indent+2); err != nil {
				return err
			}
			continue
		}
		out.WriteString(pad + "-")
		if err := emitItem(out, decoder, indent+2); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

// emitItem writes an array item; a map item puts its first key on the dash's line.
func emitItem(out *bytes.Buffer, decoder *json.Decoder, indent int) error {
	var block bytes.Buffer
	if err := emitValue(&block, decoder, indent); err != nil {
		return err
	}
	text := block.String()
	if strings.HasPrefix(text, "\n"+strings.Repeat(" ", indent+2)) {
		text = " " + strings.TrimLeft(text, "\n ")
		// The item's first line moved up to the dash; the other lines stay at the key column.
		first, rest, _ := strings.Cut(text, "\n")
		out.WriteString(first + "\n" + rest)
		return nil
	}
	out.WriteString(text)
	return nil
}

// FromYAML reads a definition file of an indexer into a spec; its id is the key. An unknown field is an error.
func FromYAML(data []byte) (Spec, error) {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return Spec{}, fmt.Errorf("spec: read yaml: %w", err)
	}
	fields["id"] = fields["key"]
	for _, key := range append(envelopeKeys[:len(envelopeKeys):len(envelopeKeys)], "key") {
		delete(fields, key)
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return Spec{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var found Spec
	if err := decoder.Decode(&found); err != nil {
		return Spec{}, fmt.Errorf("spec: decode: %w", err)
	}
	return found, nil
}

// Specs is the set a build offers: the embedded specs, with the custom ones added or replacing the embedded of the same id.
type Specs struct {
	list       []Spec
	byID       map[string]int
	custom     map[string]bool
	overridden map[string]bool
}

// Embedded is the specs the binary carries.
func Embedded() (*Specs, error) {
	all, err := All()
	if err != nil {
		return nil, err
	}
	return newSpecs(all, nil), nil
}

// With answers the set with the custom specs applied.
func (s *Specs) With(custom []Spec) *Specs { return newSpecs(s.list, custom) }

func newSpecs(embedded, custom []Spec) *Specs {
	set := &Specs{byID: make(map[string]int, len(embedded)+len(custom)), custom: map[string]bool{}, overridden: map[string]bool{}}
	for _, one := range embedded {
		set.byID[one.ID] = len(set.list)
		set.list = append(set.list, one)
	}
	for _, one := range custom {
		at, replaces := set.byID[one.ID]
		if replaces {
			set.list[at] = one
			set.overridden[one.ID] = true
		} else {
			set.byID[one.ID] = len(set.list)
			set.list = append(set.list, one)
		}
		set.custom[one.ID] = true
	}
	return set
}

// All is every spec: the embedded ones in their order, then the custom ones. The slice is shared; do not change it.
func (s *Specs) All() []Spec { return s.list }

// Get answers the spec with the id.
func (s *Specs) Get(id string) (Spec, bool) {
	at, ok := s.byID[id]
	if !ok {
		return Spec{}, false
	}
	return s.list[at], true
}

// Custom says the spec comes from a custom file; Overridden says that file replaced an embedded spec.
func (s *Specs) Custom(id string) bool     { return s.custom[id] }
func (s *Specs) Overridden(id string) bool { return s.overridden[id] }

// Library is the set currently in force; it is swapped whole when custom files are reloaded. The zero value serves the embedded set.
type Library struct{ current atomic.Pointer[Specs] }

// Specs answers the set in force; a failure to read the embedded data answers an empty set.
func (l *Library) Specs() *Specs {
	if l != nil {
		if set := l.current.Load(); set != nil {
			return set
		}
	}
	set, err := Embedded()
	if err != nil {
		return newSpecs(nil, nil)
	}
	return set
}

// Swap puts a new set in force.
func (l *Library) Swap(set *Specs) { l.current.Store(set) }
