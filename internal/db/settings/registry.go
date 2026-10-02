package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	// KindEntity is the registry's own audit-pass extension (todo 4.14, "none
	// of these fit this registry's current shape without a design pass"):
	// a whole settings surface -- quality profiles, categories, and the rest
	// of what queries/settings/*.sql shapes as real rows, never a single
	// scalar instance_setting -- registered for its tier alone. Default is
	// always empty: there is no one serializable value for "a quality
	// profile" the way there is for a port number, and nothing here reads
	// it. Declarative only: unlike a scalar key, there is no runtime
	// "unregistered key" check to add for an entity, since its own write
	// path (CreateQualityProfile and so on) is a fixed compiled-in method,
	// never a caller-supplied string that could name something unregistered.
	KindEntity SettingKind = "entity"
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
		HelpText: "Whether a caller must present a credential at all.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyBackupRetention, Kind: KindString, Tier: TierNormal,
		Name: "Keep backups by", HelpText: "count keeps the newest N backups; days keeps those younger than N days.",
		Default: string(BackupKeepCount),
	})
	registry.MustRegister(SettingDef{
		Key: KeyBackupKeepCount, Kind: KindInt, Tier: TierNormal,
		Name: "Backups to keep", HelpText: "Per database; never fewer than 3.",
		Default: strconv.Itoa(DefaultBackupKeepCount),
	})
	registry.MustRegister(SettingDef{
		Key: KeyBackupKeepDays, Kind: KindInt, Tier: TierNormal,
		Name: "Days to keep backups", HelpText: "Older backups are removed, but the newest 3 always stay.",
		Default: strconv.Itoa(DefaultBackupKeepDays),
	})
	registry.MustRegister(SettingDef{
		Key: KeyUsersNewLibraryPermission, Kind: KindString, Tier: TierNormal,
		Name:     "New libraries",
		HelpText: "What a new account is granted on libraries created after it: none, read, request or manage.",
		Default:  string(PermissionNone),
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportAutoCommitConfidence, Kind: KindFloat, Tier: TierNormal,
		Name: "Identification confidence",
		HelpText: "How likely an identification must be to be right before a scan or an import " +
			"acts on it without asking; below it, the file waits for review.",
		Default: "0.9",
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportTransferLadder, Kind: KindStringList, Tier: TierNormal,
		Name: "Import method",
		HelpText: "Methods tried in order until one succeeds: reflink, hardlink, move, " +
			"copy, and symlink only if added. Move deletes the source; symlink leaves " +
			"the library depending on it, so it is off by default (ADR-11).",
		Default: `["reflink","hardlink","move","copy"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportExtraFiles, Kind: KindBool, Tier: TierNormal,
		Name:     "Import extra files",
		HelpText: "Bring files with the listed extensions along with the tracks, as Lidarr does.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportExtraFileExtensions, Kind: KindStringList, Tier: TierNormal,
		Name:     "Extra file extensions",
		HelpText: "Comma separated, such as lrc,cue,log,nfo.",
		Default:  `["lrc","cue","log","nfo"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyImportRenameOnImport, Kind: KindBool, Tier: TierNormal,
		Name: "Rename on import",
		HelpText: "On, an imported file is named by the library's own template; " +
			"off, it keeps the name it arrived with.",
		Default: "true",
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
		Key: KeyNetworkProxyEnabled, Kind: KindBool, Tier: TierAdvanced,
		Name: "Use a proxy", HelpText: "Off until turned on: the URL below does nothing while it is.", Default: "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkProxyURL, Kind: KindString, Tier: TierAdvanced,
		Name: "Proxy", HelpText: "http, https or socks5, with an explicit port.", Default: "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkProxyBypass, Kind: KindStringList, Tier: TierAdvanced,
		Name: "Proxy bypass", HelpText: "CIDRs or host suffixes the egress proxy is skipped for.",
		Default: `["127.0.0.0/8","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkAllowCIDRsEnabled, Kind: KindBool, Tier: TierAdvanced,
		Name:     "Enforce the IP whitelist",
		HelpText: "Off until turned on, so the prefilled ranges are a suggestion until then.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyNetworkAllowCIDRs, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "IP whitelist",
		HelpText: "Once enforced this can only deny, never grant.",
		Default:  `["127.0.0.0/8","10.0.0.0/8","172.16.0.0/12","192.168.0.0/16"]`,
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

	// Recycle bin (section 10.8): the space bound, global; each library keeps its own days.
	registry.MustRegister(SettingDef{
		Key: KeyRecycleBinMaxBytes, Kind: KindInt, Tier: TierAdvanced,
		Name: "Recycle bin size limit", HelpText: "All recycle bins together, in bytes; the oldest go first. 0 is no limit.",
		Default: "0",
	})
	registry.MustRegister(SettingDef{
		Key: KeyRecycleBinMaxPercentOfRoot, Kind: KindInt, Tier: TierAdvanced,
		Name: "Recycle bin share of a root folder", HelpText: "A root's recycle bin as a percentage of the root; the oldest go first. 0 is no limit.",
		Default: fmt.Sprintf("%d", DefaultRecycleBinMaxPercentOfRoot),
	})

	// Priority (ADR-36/ADR-55): scoring rules, advanced per ADR-26's own table.
	registry.MustRegister(SettingDef{
		Key: KeyPriorityUserRequestWeight, Kind: KindInt, Tier: TierAdvanced,
		Name: "Priority weight: user request", HelpText: "Bigger is more urgent.",
		Default: fmt.Sprintf("%d", DefaultPriorityUserRequestWeight),
	})
	registry.MustRegister(SettingDef{
		Key: KeyPriorityNewReleaseWeight, Kind: KindInt, Tier: TierAdvanced,
		Name: "Priority weight: new release", HelpText: "Bigger is more urgent.",
		Default: fmt.Sprintf("%d", DefaultPriorityNewReleaseWeight),
	})
	registry.MustRegister(SettingDef{
		Key: KeyPriorityGapWeight, Kind: KindInt, Tier: TierAdvanced,
		Name: "Priority weight: gap", HelpText: "Bigger is more urgent.",
		Default: fmt.Sprintf("%d", DefaultPriorityGapWeight),
	})
	registry.MustRegister(SettingDef{
		Key: KeyPriorityUpgradeWeight, Kind: KindInt, Tier: TierAdvanced,
		Name: "Priority weight: upgrade", HelpText: "Bigger is more urgent.",
		Default: fmt.Sprintf("%d", DefaultPriorityUpgradeWeight),
	})
	registry.MustRegister(SettingDef{
		Key: KeyPriorityNewReleaseDays, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Freshness window",
		HelpText: "Days after release an item still counts as newly released rather than a gap.",
		Default:  fmt.Sprintf("%d", DefaultPriorityNewReleaseDays),
	})
	registry.MustRegister(SettingDef{
		Key: KeyRSSFreshHours, Kind: KindInt, Tier: TierAdvanced,
		Name:     "RSS freshness window",
		HelpText: "Hours after an RSS feed first shows a release for a wanted album that it still counts as newly arrived.",
		Default:  fmt.Sprintf("%d", DefaultRSSFreshHours),
	})
	registry.MustRegister(SettingDef{
		Key: KeySearchesPerHour, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Automatic searches per hour",
		HelpText: "Missing albums searched on their own each hour, most urgent first. 0 turns automatic searching off.",
		Default:  fmt.Sprintf("%d", DefaultSearchesPerHour),
	})

	registry.MustRegister(SettingDef{
		Key: KeyHuntMode, Kind: KindString, Tier: TierAdvanced,
		Name:     "Automatic search order",
		HelpText: "Which wanted albums are searched first among equally urgent ones: oldest or newest searched, oldest or newest release, or a random pick among the oldest searched.",
		Default:  "oldest_searched",
	})

	// Metadata providers: only needed while a provider filters agents, so advanced.
	registry.MustRegister(SettingDef{
		Key: KeyAcoustIDAPIKey, Kind: KindString, Tier: TierAdvanced,
		Name: "AcoustID API key",
		HelpText: "Enables fingerprint identification for files no other rung could name. " +
			"Free from acoustid.org. Stored encrypted; without one that rung is skipped.",
		Default: "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataSourceOrder, Kind: KindStringList, Tier: TierAdvanced,
		Name: "Metadata source order",
		HelpText: "Which metadata sources are tried, in order. A source left out is not " +
			"consulted; direct MusicBrainz can be reordered but not removed.",
		Default: `["brainzmash","musicbrainz-proxy","musicbrainz"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataCoverSources, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "Cover sources",
		HelpText: "Where album covers come from, in order; a source left out is not used.",
		Default:  `["coverartarchive","embedded"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataWikidata, Kind: KindBool, Tier: TierAdvanced,
		Name:     "Wikidata",
		HelpText: "Artist photos and biographies from Wikidata and Wikipedia.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataAcoustID, Kind: KindBool, Tier: TierAdvanced,
		Name:     "AcoustID fingerprints",
		HelpText: "Identify files by their sound when nothing else names them. Needs an API key.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataTheAudioDB, Kind: KindBool, Tier: TierAdvanced,
		Name:     "TheAudioDB",
		HelpText: "Artist photos and biographies, and album covers, where other sources have none.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataPeopleBackfillPerRun, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Artists filled per run",
		HelpText: "How many artists one enrichment run gives a photo and biography. Higher fills a new library sooner.",
		Default:  fmt.Sprintf("%d", DefaultPeopleBackfillPerRun),
	})
	registry.MustRegister(SettingDef{
		Key: KeyTheAudioDBAPIKey, Kind: KindString, Tier: TierAdvanced,
		Name:     "TheAudioDB API key",
		HelpText: "Optional: a supporter key from theaudiodb.com. Without one the free public key is used.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyBrainzMashUserAgent, Kind: KindString, Tier: TierAdvanced,
		Name:     "BrainzMash User-Agent",
		HelpText: "Sent to BrainzMash, whose front door refuses agents not starting \"Lidarr/\".",
		Default:  DefaultBrainzMashUserAgent,
	})

	// The non-scalar audit (todo 4.14, taken deep on the user's own word,
	// 2026-09-12): every settings surface that is real rows rather than a
	// single instance_setting value, tiered at the whole-surface level --
	// the same granularity ADR-26's own table already tiers Network and
	// Cache at, not a field-by-field pass nothing has asked for. Root
	// folders and categories are normal: every install needs at least one
	// of each (Setup's own onboarding flow creates both). The rest are
	// advanced: a shipped default quality/language profile and no arr
	// connection is a working library.
	registry.MustRegister(SettingDef{
		Key: EntityRootFolder, Kind: KindEntity, Tier: TierNormal,
		Name: "Root folders", HelpText: "Where the library actually lives.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityCategory, Kind: KindEntity, Tier: TierNormal,
		Name: "Categories", HelpText: "The policy boundary for naming, profiles and access.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityQualityProfile, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Quality profiles", HelpText: "Gates, tiers and scores that rank releases, assignable per library.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityLanguageProfile, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Language profiles", HelpText: "Section 19.8's language axis, assignable per category.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityExclusionPattern, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Unwanted file patterns", HelpText: "What a scan ignores or refuses by shape.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityArrInstance, Kind: KindEntity, Tier: TierAdvanced,
		Name: "*Arr connections", HelpText: "Importing what a Lidarr, Sonarr or similar instance wants.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityImportList, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Import lists", HelpText: "Todo 4.16: a per-library *Arr-shaped list source. Scaffolding only.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityDownloadFolder, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Download folders", HelpText: "Where a completed download is picked up from before import.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityIndexer, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Indexers", HelpText: "Newznab and Torznab search providers, Prowlarr included.",
	})
	registry.MustRegister(SettingDef{
		Key: EntityDownloader, Kind: KindEntity, Tier: TierAdvanced,
		Name: "Download clients", HelpText: "SABnzbd and other download clients.",
	})

	return registry
}

// Entity keys (todo 4.14's deep audit pass): each names a whole settings
// surface for KindEntity registration, never an instance_setting row --
// see KindEntity's own doc comment for why there is no matching Set/Get
// pair the way a scalar Key constant has.
const (
	EntityRootFolder       = "entity.root_folder"
	EntityCategory         = "entity.category"
	EntityQualityProfile   = "entity.quality_profile"
	EntityLanguageProfile  = "entity.language_profile"
	EntityExclusionPattern = "entity.exclusion_pattern"
	EntityArrInstance      = "entity.arr_instance"
	EntityImportList       = "entity.import_list"
	EntityDownloadFolder   = "entity.download_folder"
	EntityIndexer          = "entity.indexer"
	EntityDownloader       = "entity.downloader"
)
