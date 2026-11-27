package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// ErrSettingNotRegistered means a write used a key the registry lacks (ADR-26); that is a bug, surfaced as an internal error, not a validation Problem.
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
	// KindEntity registers a whole row-based settings surface (profiles, categories) for its tier alone: Default is empty and no unregistered-key check applies, since its write path is a compiled-in method.
	KindEntity SettingKind = "entity"
)

// SettingDef is one setting registered once in Go (ADR-26): key, shape, tier, default and help, the single source for the API, forms, export and validation.
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

// Registry holds every scalar setting, built once at startup; it mirrors jobs.TaskRegistry's shape by duplication because internal/jobs and this package import neither (architecture section 2).
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

// NewDefaultRegistry registers every scalar written through Cache.setPolicy, excluding schedule.<task>.* (tiered via jobs.Task.Tier) and cache.measured.* (probe facts, not policy writes).
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
	for _, image := range []struct {
		key, name, help string
		fallback        int
	}{
		{KeyCoverMaxEdge, "Cover size", "Longest edge in pixels of the largest stored picture of a library item.", DefaultCoverEdge},
		{KeyCoverQuality, "Cover quality", "WebP quality of stored pictures, 30 to 95.", DefaultCoverWebP},
		{KeyPreviewMaxEdge, "Preview size", "Longest edge in pixels of a video preview picture; pictures are read at 1280 wide.", DefaultPreviewEdge},
		{KeyPreviewQuality, "Preview quality", "JPEG quality of video previews, 30 to 95.", DefaultPreviewJPEG},
	} {
		registry.MustRegister(SettingDef{Key: image.key, Kind: KindInt, Tier: TierExpert, Name: image.name, HelpText: image.help, Default: strconv.Itoa(image.fallback)})
	}
	registry.MustRegister(SettingDef{
		Key: KeyReadyQueueLimit, Kind: KindInt, Tier: TierNormal,
		Name: "Held releases", HelpText: "How many releases wait for a free download slot; the ones that would be sent last are dropped beyond it.",
		Default: strconv.Itoa(DefaultReadyQueueLimit),
	})
	registry.MustRegister(SettingDef{
		Key: KeyVideoPerceptualHash, Kind: KindBool, Tier: TierNormal,
		Name:     "Find duplicate videos",
		HelpText: "Hashes what each video looks like in the background so the duplicate finder can match re-encodes. It reads a short run of frames at nine moments per file with ffmpeg; off, nothing new is hashed and hashes already stored keep working.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyVideoPreviews, Kind: KindBool, Tier: TierNormal,
		Name:     "Video previews",
		HelpText: "Makes a single frame (up to 720p) and a 3x3 thumbnail sheet of each video from the same frames the hashing reads, and keeps them in the previews folder under the config directory. Turning it on turns the hashing on.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyVideoPhashFrameBits, Kind: KindInt, Tier: TierNormal,
		Name:     "Duplicate video tolerance",
		HelpText: "How many of 64 bits may differ between two frames of the same moment before they count as different pictures (2 to 20). Higher finds more re-encodes and more false matches.",
		Default:  strconv.Itoa(DefaultVideoPhashFrameBits),
	})
	registry.MustRegister(SettingDef{
		Key: KeyVideoParallelFiles, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Videos hashed at once",
		HelpText: "How many video files the background hashing reads at the same time (1 to 8). One is gentle on a network share; a local disk hashes about twice as fast with three. Each file already reads three moments at once.",
		Default:  strconv.Itoa(DefaultVideoParallelFiles),
	})
	registry.MustRegister(SettingDef{
		Key: KeyHuntGaps, Kind: KindBool, Tier: TierNormal,
		Name:     "Search for old missing items",
		HelpText: "The automatic search also looks for missing items released before the freshness window. Off, only new releases and upgrades are searched; a search you start yourself works either way.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyVerifyFiles, Kind: KindBool, Tier: TierNormal,
		Name:     "Check the end of each file",
		HelpText: "The probe decodes the last seconds of each video and audio file; one that stops short is marked defective. Off skips that read.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyShowCollections, Kind: KindBool, Tier: TierNormal,
		Name:     "Show collections",
		HelpText: "Lists collections in the navigation and on item pages. Off hides them only; collections are still collected in the background.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeySolarrMode, Kind: KindString, Tier: TierNormal,
		Name: "Solarr mode",
		HelpText: "normal does everything; read_only never writes into a library; pause_downloads sends nothing to a download client; " +
			"observe is read_only with no indexer search and no download; arr_companion is observe plus handing adds to the *Arrs (which search and download) and following their monitoring.",
		Default: string(ModeNormal),
	})
	registry.MustRegister(SettingDef{
		Key: KeyScanIntervalHours, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Hours between library scans",
		HelpText: "0 lets Solarr decide: weekly for a root whose folders are watched, daily for one whose size and entry count have not changed, every 6 hours otherwise. A scan also runs at start when the last is over 12 hours old.",
		Default:  "0",
	})
	registry.MustRegister(SettingDef{
		Key: KeyScanFingerprints, Kind: KindString, Tier: TierExpert,
		Name:     "Root fingerprints at the last scan",
		HelpText: "Kept by Solarr: used space and top-level entry count of each root when it was last scanned.",
		Default:  "{}",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiskGroupOverrides, Kind: KindString, Tier: TierExpert,
		Name:     "Disk group corrections",
		HelpText: "JSON object of path to disk group label; paths with the same label share one disk's IO budget. Edited on Settings -> Disk.",
		Default:  "{}",
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
			"acts on it without asking; below it, the file waits for review. Every media type follows it: 0.90 takes " +
			"exact and near matches, 0.95 only exact ones and those settled by episode numbers, 1.0 only exact ones.",
		Default: strconv.FormatFloat(DefaultImportAutoCommitConfidence, 'f', -1, 64),
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
		Key: KeyImportRenameOnImport, Kind: KindBool, Tier: TierNormal,
		Name: "Rename on import",
		HelpText: "On, an imported file is named by the library's own template; " +
			"off, it keeps the name it arrived with.",
		Default: "true",
	})

	registry.MustRegister(SettingDef{
		Key: KeyImportMaxArchiveBytes, Kind: KindInt, Tier: TierNormal,
		Name: "Largest archive to unpack",
		HelpText: "A download kept in archives is unpacked only up to this many bytes (1 GiB to 2 TiB); " +
			"a bigger release is held and says why.",
		Default: strconv.FormatInt(DefaultImportMaxArchiveBytes, 10),
	})

	registry.MustRegister(SettingDef{
		Key: KeyImportHistoryKeepDays, Kind: KindInt, Tier: TierNormal,
		Name:     "Keep import history for",
		HelpText: "Finished imports, downloads and move jobs older than this many days (7 to 3650) are cleaned up daily; what a library file still needs stays.",
		Default:  strconv.FormatInt(DefaultImportHistoryKeepDays, 10),
	})

	// Streaming: the preview player's transcode, edited as one form (documentation/media-player.md).
	registry.MustRegister(SettingDef{
		Key: KeyStreamingTranscodeEnabled, Kind: KindBool, Tier: TierNormal,
		Name:     "Transcode for the player",
		HelpText: "Lets the built-in player convert a file its browser cannot play with ffmpeg. Off, only files the browser plays as they are can be previewed.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyStreamingHardware, Kind: KindString, Tier: TierAdvanced,
		Name:     "Transcode hardware",
		HelpText: "auto uses the best GPU encoder found working (NVIDIA, then VAAPI for Intel and AMD, then Intel QSV); software never uses a GPU.",
		Default:  StreamingHardwareAuto,
	})
	registry.MustRegister(SettingDef{
		Key: KeyStreamingDevice, Kind: KindString, Tier: TierAdvanced,
		Name:     "Transcode render device",
		HelpText: "The render node VAAPI and QSV use, such as /dev/dri/renderD128. Empty takes the one detection found.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyStreamingMaxHeight, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Transcode maximum height",
		HelpText: "Video taller than this is scaled down while transcoding, whatever quality the viewer picked. 0 sets no cap.",
		Default:  strconv.Itoa(DefaultStreamingMaxHeight),
	})
	registry.MustRegister(SettingDef{
		Key: KeyStreamingVideoKbps, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Transcode video bitrate",
		HelpText: "Video bitrate in kbit/s for a converted original whose own bitrate is unknown; a quality profile sets its own.",
		Default:  strconv.Itoa(DefaultStreamingVideoKbps),
	})
	registry.MustRegister(SettingDef{
		Key: KeyStreamingMaxSessions, Kind: KindInt, Tier: TierAdvanced,
		Name:     "Simultaneous transcodes",
		HelpText: "How many conversions run at once; a further one is refused until one ends.",
		Default:  strconv.Itoa(DefaultStreamingMaxSessions),
	})

	// Network (ADR-58) is advanced: someone behind a reverse proxy or LAN restriction tunes it, a working library needs no change, and it is policy, not expert tuning.
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
	registry.MustRegister(SettingDef{
		Key: KeyCacheMinFreePercent, Kind: KindInt, Tier: TierExpert,
		Name:     "Cache: disk kept free",
		HelpText: "Percentage of the disk under the cache kept free; the cache shrinks below its size limit to keep it. 0 is off.",
		Default:  fmt.Sprintf("%d", DefaultCacheMinFreePercent),
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
	registry.MustRegister(SettingDef{
		Key: KeyRecycleBinMinFreeBytes, Kind: KindInt, Tier: TierAdvanced,
		Name: "Recycle bin: minimum free space", HelpText: "A root with less free space than this, in bytes, has its recycle bin emptied oldest first until it has it. 0 is no floor.",
		Default: "0",
	})
	registry.MustRegister(SettingDef{
		Key: KeyRecycleBinMinFreePercent, Kind: KindInt, Tier: TierAdvanced,
		Name: "Recycle bin: minimum free share", HelpText: "The same floor as a percentage of the root; the larger of the two counts. 0 is no floor.",
		Default: "0",
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

	for key, share := range map[string]struct {
		name  string
		value int64
	}{
		KeyRequestShareUser:       {"Request share: what I am doing", DefaultRequestShares.User},
		KeyRequestShareFresh:      {"Request share: new content", DefaultRequestShares.Fresh},
		KeyRequestShareBackground: {"Request share: background fill", DefaultRequestShares.Background},
	} {
		registry.MustRegister(SettingDef{
			Key: key, Kind: KindInt, Tier: TierAdvanced, Name: share.name, Default: fmt.Sprintf("%d", share.value),
			HelpText: "Outbound requests to a busy provider are served in these proportions when all three kinds are waiting; a kind nobody waits for gives its part to the others.",
		})
	}
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
		Key: KeyMetadataTVSources, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "TV source order",
		HelpText: "Which TV provider supplies a series' text, genres, rating and artwork first; one left out is not used. Numbering stays with TheTVDB's.",
		Default:  `["wikidata","skyhook","tmdb","tvdb","tvmaze","anilist"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataMovieSources, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "Movie source order",
		HelpText: "Which movie provider supplies a film's text, ratings and artwork first; the next fills what is missing, one left out is not used.",
		Default:  `["wikidata","tmdb","radarr"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataDisabledProviders, Kind: KindStringList, Tier: TierNormal,
		Name:     "Metadata providers switched off",
		HelpText: "Providers no library asks; their keys stay stored. Google Books starts here: without a key of your own it answers 429.",
		Default:  `["` + BookSourceGoogleBooks + `"]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyLibraryTypeDefaults, Kind: KindString, Tier: TierNormal,
		Name:     "Library type defaults",
		HelpText: "Per media type: the metadata providers and their order, the target quality, the language and the quality profiles a library starts with. A library's own choice wins.",
		Default:  `{}`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataProviderPaces, Kind: KindStringList, Tier: TierAdvanced,
		Name:     "Metadata provider paces",
		HelpText: "Requests a minute a person set for a provider, \"provider:pace\" each; the rest keep their shipped pace.",
		Default:  `[]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeEnabled, Kind: KindBool, Tier: TierNormal,
		Name:     "Transcode",
		HelpText: "Convert library files to their target quality in the background (off by default).",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeParallelAuto, Kind: KindBool, Tier: TierNormal,
		Name:     "Transcode: parallel conversions follow the hardware",
		HelpText: "The lane limits are worked out from the cores and encode engines found, until you set limits of your own.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodePlaybackParallelAuto, Kind: KindBool, Tier: TierNormal,
		Name:     "Transcode while playing: limits follow the hardware",
		HelpText: "The lane limits while the web player converts are worked out from the cores and encode engines found, until you set limits of your own.",
		Default:  "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeParallelCPU, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: parallel software conversions",
		HelpText: "How many conversions on the CPU run at once.",
		Default:  "1",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeParallelGPU, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: parallel hardware conversions",
		HelpText: "How many conversions on a GPU run at once.",
		Default:  "2",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeParallelAudio, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: parallel audio conversions",
		HelpText: "How many audio-only conversions run at once.",
		Default:  "4",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeParallelBooks, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: parallel book conversions",
		HelpText: "How many comic repacks and ebook conversions run at once, counted with every lane before it.",
		Default:  "4",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodePlaybackParallelBooks, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode while playing: books",
		HelpText: "How many book conversions may run while the web player converts.",
		Default:  "1",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodePlaybackParallelCPU, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode while playing: software",
		HelpText: "How many CPU conversions may run while the web player converts.",
		Default:  "0",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodePlaybackParallelGPU, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode while playing: hardware",
		HelpText: "How many GPU conversions may run while the web player converts.",
		Default:  "1",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodePlaybackParallelAudio, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode while playing: audio",
		HelpText: "How many audio conversions may run while the web player converts.",
		Default:  "1",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeOriginalsRetainDays, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: days an original is kept",
		HelpText: "Days a replaced original stays in the recycle bin; 0 follows the bin's normal rule.",
		Default:  "7",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTranscodeOriginalsCapGB, Kind: KindInt, Tier: TierNormal,
		Name:     "Transcode: originals size cap",
		HelpText: "Gigabytes replaced originals may take together before the oldest are purged; 0 for no cap.",
		Default:  "100",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverEnabled, Kind: KindBool, Tier: TierNormal,
		Name:     "Discover",
		HelpText: "Recommendations of works that are not in Solarr or not monitored: a Discover page, a dashboard row and, per library, optional auto-add.",
		Default:  "false",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverExcludeGenres, Kind: KindStringList, Tier: TierNormal,
		Name:     "Discover: genres never recommended",
		HelpText: "Genres no library recommends from, on top of each library's own list.",
		Default:  `[]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverExcludeNetworks, Kind: KindStringList, Tier: TierNormal,
		Name:     "Discover: networks never recommended",
		HelpText: "Networks and studios no library recommends from, on top of each library's own list.",
		Default:  `[]`,
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverClassicYears, Kind: KindInt, Tier: TierNormal,
		Name: "Discover: a classic is older than (years)", HelpText: "How old a title must be to appear in the Classics list.", Default: "25",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverMinRating, Kind: KindFloat, Tier: TierNormal,
		Name: "Discover: auto-add minimum rating", HelpText: "Auto-add only takes what its source rates at least this, 0 to 10.", Default: "7",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverMinVotes, Kind: KindInt, Tier: TierNormal,
		Name: "Discover: auto-add minimum votes", HelpText: "Auto-add only takes what at least this many people rated.", Default: "1000",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverAutoAddPerDay, Kind: KindInt, Tier: TierNormal,
		Name: "Discover: auto-add at most per day", HelpText: "The most auto-add adds to one library in a day.", Default: "5",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscoverAutoAddSearch, Kind: KindBool, Tier: TierNormal,
		Name: "Discover: search for what auto-add adds", HelpText: "Start looking for each title as soon as auto-add has added it.", Default: "true",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetadataLanguage, Kind: KindString, Tier: TierNormal,
		Name:     "Metadata language",
		HelpText: "The language titles and overviews are asked in by every provider that takes one; English is used where a provider has nothing in it.",
		Default:  DefaultMetadataLanguage,
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
	for _, grace := range []struct {
		key, name, help string
		hours           int64
	}{
		{KeyAvailabilityGraceHigh, "Wait after an exact air time", "Hours after an episode's exact air time before it counts as missing and is searched for.", DefaultAvailabilityGrace.High},
		{KeyAvailabilityGraceMedium, "Wait after a likely air time", "Hours after an air time a provider worked out (not stated) before the episode counts as missing.", DefaultAvailabilityGrace.Medium},
		{KeyAvailabilityGraceLow, "Wait after an air date", "Hours after the start of an air date with no time before the episode counts as missing.", DefaultAvailabilityGrace.Low},
	} {
		registry.MustRegister(SettingDef{
			Key: grace.key, Kind: KindInt, Tier: TierAdvanced, Name: grace.name, HelpText: grace.help,
			Default: strconv.FormatInt(grace.hours, 10),
		})
	}
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
		Key: KeyMDBListAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "MDBList key",
		HelpText: "Your own free key from mdblist.com: every site's rating of a film in one request.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyOMDbAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "OMDb key",
		HelpText: "Your own free key from omdbapi.com: IMDb, Rotten Tomatoes and Metacritic ratings and English plots.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyFanartAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "fanart.tv key",
		HelpText: "Your own free personal key from fanart.tv: clear logos, clear art, disc art and banners.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyGoogleBooksAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "Google Books key",
		HelpText: "Optional: your own free key from the Google Cloud console. Without one Google Books answers on a small quota everyone shares.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyHardcoverAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "Hardcover token",
		HelpText: "Your own free API token from hardcover.app/account/api: descriptions, ratings, genres and covers of books.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyStashDBAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "StashDB key",
		HelpText: "Your own API key from your StashDB account (stashdb.org): adult scenes, performers, studios and tags.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyThePornDBAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "ThePornDB key",
		HelpText: "Your own API key from your ThePornDB account (theporndb.net): adult scenes, performers and sites.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyFansDBAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "FansDB key",
		HelpText: "Your own API key from your FansDB account (fansdb.cc): fan-platform scenes and creators.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyComicVineAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "ComicVine key",
		HelpText: "Your own API key from your ComicVine account (comicvine.gamespot.com/api): comic series, issues and credits.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMetronAccount, Kind: KindString, Tier: TierNormal,
		Name:     "Metron account",
		HelpText: "Your Metron account as user:password (metron.cloud): comic series, issues, page counts and reprints.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyLastFMAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "Last.fm key",
		HelpText: "Your own API key from last.fm/api (free): how many people listen to an album, and similar artists.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyDiscogsToken, Kind: KindString, Tier: TierNormal,
		Name:     "Discogs token",
		HelpText: "Your own personal access token from discogs.com/settings/developers: the community's rating of a release.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeySpotifyClient, Kind: KindString, Tier: TierNormal,
		Name:     "Spotify app",
		HelpText: "Your Spotify app as client id:client secret (developer.spotify.com, free): an album's popularity and covers.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyMALClientID, Kind: KindString, Tier: TierNormal,
		Name:     "MyAnimeList client id",
		HelpText: "Your own client id from myanimelist.net/apiconfig (free): anime scores, synopses and posters.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyAniDBClient, Kind: KindString, Tier: TierNormal,
		Name:     "AniDB client name",
		HelpText: "The client name you registered at anidb.net (Account > API): anime ratings and descriptions. AniDB bans a client that asks too fast, so it is asked slowly.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTMDBAPIKey, Kind: KindString, Tier: TierNormal,
		Name:     "TMDB key",
		HelpText: "Your own TMDB read access token or API key (themoviedb.org, free). Adds cast, crew, guest stars, artwork and alternative orderings to TV.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyTVDBAPIKey, Kind: KindString, Tier: TierAdvanced,
		Name:     "TheTVDB key",
		HelpText: "Optional: your own TheTVDB v4 project key. Without one the built-in project key is used.",
		Default:  "",
	})
	registry.MustRegister(SettingDef{
		Key: KeyBrainzMashUserAgent, Kind: KindString, Tier: TierAdvanced,
		Name:     "BrainzMash User-Agent",
		HelpText: "Sent to BrainzMash, whose front door refuses agents not starting \"Lidarr/\".",
		Default:  DefaultBrainzMashUserAgent,
	})

	// Non-scalar surfaces are tiered whole (todo 4.14): root folders and categories are normal since setup creates both; the rest are advanced because shipped defaults work.
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

// Entity keys name a whole KindEntity surface, never an instance_setting row, so they have no Set/Get pair.
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
