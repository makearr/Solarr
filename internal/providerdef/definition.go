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
	PerMinute    int64 `yaml:"per_minute,omitempty"`
	MaxPerMinute int64 `yaml:"max_per_minute,omitempty"`
}

// SettingSpec declares a value a definition needs from its owner; its value is sealed in the settings database, never in the file.
type SettingSpec struct {
	Key   string `yaml:"key"`
	Label string `yaml:"label"`
	Type  string `yaml:"type"`
}

// Credentials are what an indexer asks its owner for, shown by a descriptor and never tunable.
type Credentials struct {
	UserLabel   string `yaml:"user_label,omitempty"`
	SecretLabel string `yaml:"secret_label,omitempty"`
	Optional    bool   `yaml:"optional,omitempty"`
}

// File is every field the envelope and the tunable descriptor carry; the body of an http or spec definition is read by its engine.
// The fields after Settings only describe a compiled provider: a custom descriptor changing them is warned about and ignored.
type File struct {
	Schema       int           `yaml:"schema"`
	Kind         Kind          `yaml:"kind"`
	Key          string        `yaml:"key"`
	Name         string        `yaml:"name"`
	Engine       Engine        `yaml:"engine"`
	Enabled      *bool         `yaml:"enabled,omitempty"`
	Description  string        `yaml:"description,omitempty"`
	Hosts        []string      `yaml:"hosts,omitempty"`
	Pace         *Pace         `yaml:"pace,omitempty"`
	MediaTypes   []string      `yaml:"media_types,omitempty"`
	BaseURLs     []string      `yaml:"base_urls,omitempty"`
	RequestDelay string        `yaml:"request_delay,omitempty"`
	Settings     []SettingSpec `yaml:"settings,omitempty"`
	NeedsKey     bool          `yaml:"needs_key,omitempty"`
	Carries      []string      `yaml:"carries,omitempty"`
	AlsoFor      []string      `yaml:"also_for,omitempty"`
	Published    string        `yaml:"published,omitempty"`
	Role         string        `yaml:"role,omitempty"`
	Impl         string        `yaml:"impl,omitempty"`
	Privacy      string        `yaml:"privacy,omitempty"`
	Protocol     string        `yaml:"protocol,omitempty"`
	Language     string        `yaml:"language,omitempty"`
	Companion    string        `yaml:"companion_downloader,omitempty"`
	Credentials  *Credentials  `yaml:"credentials,omitempty"`
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
