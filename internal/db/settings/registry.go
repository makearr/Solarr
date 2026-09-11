package settings

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrSettingNotRegistered is what a write path returns for a key the
// registry has never heard of (ADR-26: "a setting not in the registry
// cannot be written"). Reaching this in production is a bug -- every key an
// API write path uses must be registered alongside it -- not a user input
// problem, so callers surface it as an internal error rather than a
// validation Problem.
var ErrSettingNotRegistered = errors.New("settings: key is not registered")

// SettingKind is the shape a registered setting's value takes -- enough for
// a form to pick a control and for a future validator to parse the raw
// instance_setting value_json back into it.
type SettingKind string

// Kinds as of this registry's first slice (todo 4.14). Grows as a setting
// needs a shape none of these cover.
const (
	KindString     SettingKind = "string"
	KindInt        SettingKind = "int"
	KindFloat      SettingKind = "float"
	KindBool       SettingKind = "bool"
	KindStringList SettingKind = "string_list"
)

// SettingDef is one setting registered once in Go (ADR-26): its key, shape,
// tier, default and help text -- the single source the API, the forms and,
// eventually, the YAML export and write-time validation are meant to read
// from instead of each inventing their own copy.
type SettingDef struct {
	// Key matches the instance_setting row this setting is stored under.
	Key  string
	Kind SettingKind
	Tier Tier
	// Name is shown on the settings screen. Plain text today, the same
	// deferred i18n note jobs.Task.Name carries.
	Name string
	// HelpText explains what the setting does and why it might be changed.
	HelpText string
	// Default is the encoded fallback: the same representation
	// SetInstanceSetting stores (a JSON array for KindStringList, decimal
	// text otherwise) so a form can show it without a second parser.
	Default string
}

// Registry holds every scalar setting declared this way. One
// instance for the whole process, built once at startup --
// jobs.TaskRegistry's own shape, duplicated rather than shared: internal/jobs
// and internal/db/settings import neither of each other (architecture §2),
// and a setting's own extra fields (kind, default, help text) do not fit
// a task's shape anyway.
type Registry struct {
	mu   sync.RWMutex
	defs map[string]SettingDef
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{defs: make(map[string]SettingDef)}
}

// Register adds a setting, refusing a duplicate key or an incomplete definition.
func (r *Registry) Register(def SettingDef) error {
	if def.Key == "" {
		return fmt.Errorf("settings: setting has no key")
	}
	if def.Kind == "" {
		return fmt.Errorf("settings: setting %s has no kind", def.Key)
	}
	switch def.Tier {
	case TierNormal, TierAdvanced, TierExpert:
	default:
		return fmt.Errorf("settings: setting %s has no valid tier: %q", def.Key, def.Tier)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.defs[def.Key]; exists {
		return fmt.Errorf("settings: setting %s is already registered", def.Key)
	}
	r.defs[def.Key] = def
	return nil
}

// MustRegister panics on a duplicate or invalid definition. For startup
// wiring, where there is no caller to return an error to and the failure is
// always a bug.
func (r *Registry) MustRegister(def SettingDef) {
	if err := r.Register(def); err != nil {
		panic(err)
	}
}

// Lookup returns one setting definition by key.
func (r *Registry) Lookup(key string) (SettingDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	def, found := r.defs[key]
	return def, found
}

// LookupAll reports whether every key is registered -- the shape a write
// path that touches several keys at once (network, cache budget) needs.
func (r *Registry) LookupAll(keys ...string) error {
	for _, key := range keys {
		if _, found := r.Lookup(key); !found {
			return fmt.Errorf("%w: %s", ErrSettingNotRegistered, key)
		}
	}
	return nil
}

// All lists every registered setting, sorted by key, for the API and the
// settings screen.
func (r *Registry) All() []SettingDef {
	r.mu.RLock()
	defer r.mu.RUnlock()

	defs := make([]SettingDef, 0, len(r.defs))
	for _, def := range r.defs {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Key < defs[j].Key })
	return defs
}

// NewDefaultRegistry registers every scalar setting this process currently
// writes through Cache.setPolicy's shared chokepoint. Deliberately excludes
// two things that flow through the same table but are not this registry's
// concern: schedule.<task>.cadence/enabled, dynamic per registered task and
// already tier-tagged via jobs.Task.Tier (gated by jobs.TaskRegistry.Lookup
// in the adapter, not here); and cache.measured.*, a probe's own facts,
// never a policy write (ADR-44 governs user input, not what a sweep found).
func NewDefaultRegistry() *Registry {
	registry := NewRegistry()

	registry.MustRegister(SettingDef{
		Key: KeyAuthRequired, Kind: KindBool, Tier: TierNormal,
		Name:     "Require login",
		HelpText: "Whether a caller must present a credential at all (ADR-14).",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportAutoCommitConfidence, Kind: KindFloat, Tier: TierNormal,
		Name: "Import commit line",
		HelpText: "The confidence a unit's identification must clear to import " +
			"automatically rather than wait in the manual queue (ADR-48).",
		Default: "0.9",
	})

	// Network (ADR-58): self-hosted networking, tuned by someone running
	// this behind a reverse proxy or restricting it to a LAN -- advanced,
	// not normal (a working library needs none of it changed) and not
	// expert (it is user-facing policy, not internal tuning).
	registry.MustRegister(SettingDef{
		Key: KeyNetworkPort, Kind: KindInt, Tier: TierAdvanced,
		Name: "Port", HelpText: "0 (unset) defers to config.yml/the built-in default.", Default: "0",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkProxyURL, Kind: KindString, Tier: TierAdvanced,
		Name: "Egress proxy", HelpText: "http, https or socks5, with an explicit port.", Default: "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkProxyBypass, Kind: KindStringList, Tier: TierAdvanced,
		Name: "Proxy bypass", HelpText: "CIDRs or host suffixes the egress proxy is skipped for.", Default: "[]",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkAllowCIDRs, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "IP allowlist",
		HelpText: "Empty allows every address; non-empty can only deny (Invariant 89).",
		Default:  "[]",
	})

	// Cache (ADR-60): sizing and database maintenance -- expert, per
	// ADR-26's own table.
	registry.MustRegister(SettingDef{
		Key: KeyCacheMaxBytes, Kind: KindInt, Tier: TierExpert,
		Name: "Cache size limit", HelpText: "Total byte ceiling across every namespace.",
		Default: fmt.Sprintf("%d", DefaultCacheMaxBytes),
	})
	registry.MustRegister(SettingDef{
		Key: KeyCacheMaxAgeDays, Kind: KindInt, Tier: TierExpert,
		Name: "Cache entry age limit", HelpText: "Per-entry age ceiling, in days.",
		Default: fmt.Sprintf("%d", DefaultCacheMaxAgeDays),
	})
	registry.MustRegister(SettingDef{
		Key: KeyCacheImagesShare, Kind: KindInt, Tier: TierExpert,
		Name:     "Cache share: images",
		HelpText: "Percentage of the cache size limit reserved for images. Must sum to 100 with the metadata share.",
		Default:  fmt.Sprintf("%d", DefaultCacheImagesSharePercent),
	})
	registry.MustRegister(SettingDef{
		Key: KeyCacheMetadataShare, Kind: KindInt, Tier: TierExpert,
		Name:     "Cache share: metadata",
		HelpText: "Percentage of the cache size limit reserved for metadata. Must sum to 100 with the images share.",
		Default:  fmt.Sprintf("%d", DefaultCacheMetadataSharePercent),
	})

	return registry
}
