// Package providerdef holds provider definition files: YAML data that describes or is a provider, never code.
// A built-in set ships embedded and is mirrored to the config folder; a person's own files live beside it and are
// validated strictly, one file at a time, so a bad file harms only itself.
package providerdef

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

// SchemaVersion is the newest file schema this build reads.
const SchemaVersion = 1

// Limits bound what one definition file and one folder may be (the safety model of the plan).
const (
	MaxFileBytes    = 256 << 10
	MaxFilesPerKind = 1000
)

// Kind is what a definition describes; it must match the folder the file is in.
type Kind string

// The four kinds.
const (
	KindMetadata Kind = "metadata"
	KindIndexer  Kind = "indexer"
	KindDownload Kind = "download"
	KindWebhook  Kind = "webhook"
)

// Kinds lists every kind in folder order.
var Kinds = []Kind{KindMetadata, KindIndexer, KindDownload, KindWebhook}

// Engine says whether a definition describes a compiled provider or is the provider.
type Engine string

// The three engines: builtin tunes a compiled provider, http is a declarative HTTP provider, spec is the indexer engine.
const (
	EngineBuiltin Engine = "builtin"
	EngineHTTP    Engine = "http"
	EngineSpec    Engine = "spec"
)

// Source says where a definition came from.
type Source string

// Built-in definitions come from the embedded data, custom ones from the person's folder.
const (
	SourceBuiltin Source = "builtin"
	SourceCustom  Source = "custom"
)

// Pace is a provider's request pace in requests a minute; MaxPerMinute is the compiled ceiling of a built-in one.
type Pace struct {
	PerMinute    int64 `yaml:"per_minute"`
	MaxPerMinute int64 `yaml:"max_per_minute"`
}

// SettingSpec declares a value a definition needs from its owner; its value is sealed in the settings database, never in the file.
type SettingSpec struct {
	Key   string `yaml:"key"`
	Label string `yaml:"label"`
	Type  string `yaml:"type"`
}

// File is every field the envelope and the tunable descriptor carry; the body of an http or spec definition is read by its engine.
type File struct {
	Schema       int           `yaml:"schema"`
	Kind         Kind          `yaml:"kind"`
	Key          string        `yaml:"key"`
	Name         string        `yaml:"name"`
	Engine       Engine        `yaml:"engine"`
	Enabled      *bool         `yaml:"enabled"`
	Description  string        `yaml:"description"`
	Hosts        []string      `yaml:"hosts"`
	Pace         *Pace         `yaml:"pace"`
	MediaTypes   []string      `yaml:"media_types"`
	BaseURLs     []string      `yaml:"base_urls"`
	RequestDelay string        `yaml:"request_delay"`
	Settings     []SettingSpec `yaml:"settings"`
}

// Definition is one parsed file.
type Definition struct {
	File
	Source Source
	// Hash is the SHA-256 of the file's bytes.
	Hash string
	// Overrides is set on a custom definition whose key a built-in one also has.
	Overrides bool
	// Raw is the file as read, for an engine to decode its body from.
	Raw []byte
}

// EnabledByDefault is what the file says; a definition that says nothing is on.
func (d Definition) EnabledByDefault() bool { return d.Enabled == nil || *d.Enabled }

// NormalHosts lists the declared hosts lower-cased, sorted and without duplicates.
func (d Definition) NormalHosts() []string {
	hosts := make([]string, 0, len(d.Hosts))
	for _, host := range d.Hosts {
		hosts = append(hosts, strings.ToLower(strings.TrimSpace(host)))
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

func hashOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
