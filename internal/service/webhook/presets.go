package webhook

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"

	"github.com/goccy/go-yaml"
)

//go:embed presets/*.yaml
var presetFiles embed.FS

// Preset shapes the request for a service: method, headers, content type and a body template over the payload.
type Preset struct {
	Key         string            `yaml:"key"`
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Method      string            `yaml:"method"`
	ContentType string            `yaml:"content_type"`
	Headers     map[string]string `yaml:"headers"`
	Body        string            `yaml:"body"`
}

var bodyFuncs = template.FuncMap{
	// json quotes a value as a JSON string, so a title with quotes or a newline cannot break the body.
	"json": func(value string) (string, error) {
		encoded, err := json.Marshal(value)
		return string(encoded), err
	},
}

// PresetFiles are the shipped preset files by key, for the definitions mirror.
func PresetFiles() map[string][]byte {
	files := map[string][]byte{}
	entries, err := fs.ReadDir(presetFiles, "presets")
	if err != nil {
		return files
	}
	for _, entry := range entries {
		if data, err := presetFiles.ReadFile("presets/" + entry.Name()); err == nil {
			files[strings.TrimSuffix(entry.Name(), ".yaml")] = data
		}
	}
	return files
}

// Presets lists the shipped presets by key order.
func Presets() []Preset {
	var out []Preset
	for _, data := range PresetFiles() {
		var preset Preset
		if err := yaml.Unmarshal(data, &preset); err == nil {
			out = append(out, preset)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// PresetByKey finds a preset.
func PresetByKey(key string) (Preset, bool) {
	for _, preset := range Presets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return Preset{}, false
}

// Render makes the request body of a payload for the preset.
func (p Preset) Render(payload Payload) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	parsed, err := template.New(p.Key).Funcs(bodyFuncs).Parse(p.Body)
	if err != nil {
		return nil, fmt.Errorf("webhook: preset %s: %w", p.Key, err)
	}
	var out bytes.Buffer
	if err := parsed.Execute(&out, map[string]string{"Raw": string(raw), "Line": payload.line()}); err != nil {
		return nil, fmt.Errorf("webhook: preset %s: %w", p.Key, err)
	}
	return out.Bytes(), nil
}

// SamplePayload is an invented "imported" event, for showing what a preset sends before anything has happened.
func SamplePayload() Payload {
	return payloadOf(Occurrence{
		Event:   EventImported,
		Library: Library{ID: 2, Name: "Comics", MediaType: "comic", Kind: "comic"},
		Work:    &Work{ID: 41, Title: "Example Series", Year: 2024},
		Files:   []File{{Name: "Example Series 001.cbz", Size: 52428800}},
	}, Instance{Name: "Solarr", Version: "example"}, 1767225600, false)
}

// Sample is the body this preset sends for SamplePayload.
func (p Preset) Sample() string {
	body, err := p.Render(SamplePayload())
	if err != nil {
		return ""
	}
	return string(body)
}
