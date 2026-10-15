package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Document is configuration a person can read, diff and share (F9). Policy
// only, matched on natural keys. See documentation/configuration-export.md.
type Document struct {
	Version           int                     `yaml:"version"`
	Settings          *ScalarSettingsEntry    `yaml:"settings,omitempty"`
	Categories        []CategoryEntry         `yaml:"categories,omitempty"`
	Roots             []RootEntry             `yaml:"root_folders,omitempty"`
	QualityProfiles   []QualityProfileEntry   `yaml:"quality_profiles,omitempty"`
	QualityTargets    []QualityTargetEntry    `yaml:"quality_targets,omitempty"`
	LanguageProfiles  []LanguageProfileEntry  `yaml:"language_profiles,omitempty"`
	ExclusionPatterns []ExclusionPatternEntry `yaml:"exclusion_patterns,omitempty"`
	// Schedules holds each task's stored overrides by task key; a task absent here runs on its defaults.
	Schedules map[string]ScheduleEntry `yaml:"schedules,omitempty"`
}

// ScalarSettingsEntry is every todo-4.14-registered scalar setting (ADR-21,
// ADR-26's own registry), flat and named for readability rather than
// mirroring instance_setting's dotted keys. A pointer on Document: nil on
// import means the document says nothing about them, so Import leaves every
// one alone (a hand-edited document need not repeat every setting just to
// change a category); Export always populates it, since these are
// always-existing global singletons, never legitimately absent.
type ScalarSettingsEntry struct {
	AuthRequired               bool     `yaml:"auth_required"`
	ImportAutoCommitConfidence float64  `yaml:"import_auto_commit_confidence"`
	ImportTransferLadder       []string `yaml:"import_transfer_ladder,omitempty"`
	ImportRenameOnImport       bool     `yaml:"import_rename_on_import"`
	NetworkPort                int      `yaml:"network_port,omitempty"`
	NetworkProxyEnabled        bool     `yaml:"network_proxy_enabled"`
	NetworkProxyURL            string   `yaml:"network_proxy_url,omitempty"`
	NetworkProxyBypass         []string `yaml:"network_proxy_bypass,omitempty"`
	NetworkAllowCIDRsEnabled   bool     `yaml:"network_allow_cidrs_enabled"`
	NetworkAllowCIDRs          []string `yaml:"network_allow_cidrs,omitempty"`
	// RecycleBinMaxBytes and its share of a root; absent keeps what is stored.
	RecycleBinMaxBytes         *int64 `yaml:"recycle_bin_max_bytes,omitempty"`
	RecycleBinMaxPercentOfRoot *int64 `yaml:"recycle_bin_max_percent_of_root,omitempty"`
	CacheMaxBytes              int64  `yaml:"cache_max_bytes,omitempty"`
	CacheMaxAgeDays            int64  `yaml:"cache_max_age_days,omitempty"`
	CacheImagesSharePercent    int64  `yaml:"cache_images_share_percent,omitempty"`
	CacheMetadataSharePercent  int64  `yaml:"cache_metadata_share_percent,omitempty"`
	PriorityUserRequestWeight  int64  `yaml:"priority_user_request_weight,omitempty"`
	PriorityNewReleaseWeight   int64  `yaml:"priority_new_release_weight,omitempty"`
	PriorityGapWeight          int64  `yaml:"priority_gap_weight,omitempty"`
	PriorityUpgradeWeight      int64  `yaml:"priority_upgrade_weight,omitempty"`
	PriorityNewReleaseDays     int64  `yaml:"priority_new_release_days,omitempty"`
	// AutomaticSearchesPerHour is the search budget; absent keeps the stored value.
	AutomaticSearchesPerHour *int64   `yaml:"automatic_searches_per_hour,omitempty"`
	RSSFreshHours            *int64   `yaml:"rss_fresh_hours,omitempty"`
	BrainzMashUserAgent      string   `yaml:"brainzmash_user_agent,omitempty"`
	MetadataSourceOrder      []string `yaml:"metadata_source_order,omitempty"`
	// Pointers, so "all cover sources off" and "switched off" survive a round trip; absent keeps
	// the stored value.
	MetadataCoverSources *[]string `yaml:"metadata_cover_sources,omitempty"`
	MetadataTVSources    *[]string `yaml:"metadata_tv_sources,omitempty"`
	MetadataWikidata     *bool     `yaml:"metadata_wikidata,omitempty"`
	MetadataAcoustID     *bool     `yaml:"metadata_acoustid,omitempty"`
	MetadataTheAudioDB   *bool     `yaml:"metadata_theaudiodb,omitempty"`
	// MetadataPeopleBackfillPerRun is how many artists one run backfills; absent keeps the stored value.
	MetadataPeopleBackfillPerRun *int64 `yaml:"metadata_people_backfill_per_run,omitempty"`
	// AvailabilityGraceHours is the wait after a part's time by how sure it is; absent keeps the stored values.
	AvailabilityGraceHours *AvailabilityGraceEntry `yaml:"availability_grace_hours,omitempty"`
	// UsersNewLibraryPermission is what new accounts are granted on later libraries; empty keeps it.
	UsersNewLibraryPermission Permission `yaml:"users_new_library_permission,omitempty"`
	// Backup retention; an empty mode keeps all three as stored.
	BackupRetention BackupRetentionMode `yaml:"backup_retention,omitempty"`
	BackupKeepCount int64               `yaml:"backup_keep_count,omitempty"`
	BackupKeepDays  int64               `yaml:"backup_keep_days,omitempty"`

	// present is the keys a parsed document named (NameSettings); nil means every one, as in an
	// export or a document built in Go.
	present map[string]bool
}

// settingsGroups are keys that only mean something together: naming one asks for the others.
var settingsGroups = [][]string{
	{"network_proxy_enabled", "network_allow_cidrs_enabled"},
	{"cache_max_bytes", "cache_max_age_days", "cache_images_share_percent", "cache_metadata_share_percent"},
	{"priority_user_request_weight", "priority_new_release_weight", "priority_gap_weight",
		"priority_upgrade_weight", "priority_new_release_days"},
}

// networkGroupKeys are every network key; any of them brings in the whole network group.
var networkGroupKeys = []string{"network_port", "network_proxy_enabled", "network_proxy_url",
	"network_proxy_bypass", "network_allow_cidrs_enabled", "network_allow_cidrs"}

// NameSettings records which keys a hand-written settings block names, so a key it leaves out
// keeps the stored value rather than reading as false or zero (found 2026-09-24: an absent
// auth_required would have switched sign-in off).
func (d *Document) NameSettings(keys []string) {
	if d.Settings == nil {
		return
	}
	d.Settings.present = make(map[string]bool, len(keys))
	for _, key := range keys {
		d.Settings.present[key] = true
	}
}

func (e ScalarSettingsEntry) has(key string) bool { return e.present == nil || e.present[key] }

// recycleBinLimits is the document's recycle bin limits, a missing one at its default; nil when neither is named.
func (e ScalarSettingsEntry) recycleBinLimits() *RecycleBinLimits {
	if e.RecycleBinMaxBytes == nil && e.RecycleBinMaxPercentOfRoot == nil {
		return nil
	}
	limits := RecycleBinLimits{MaxPercentOfRoot: DefaultRecycleBinMaxPercentOfRoot}
	if e.RecycleBinMaxBytes != nil {
		limits.MaxBytes = *e.RecycleBinMaxBytes
	}
	if e.RecycleBinMaxPercentOfRoot != nil {
		limits.MaxPercentOfRoot = *e.RecycleBinMaxPercentOfRoot
	}
	return &limits
}

// hasAll is whether a group is complete; its values are judged only then, the missing keys first.
func (e ScalarSettingsEntry) hasAll(keys []string) bool {
	for _, key := range keys {
		if !e.has(key) {
			return false
		}
	}
	return true
}

func (e ScalarSettingsEntry) hasAny(keys []string) bool {
	for _, key := range keys {
		if e.has(key) {
			return true
		}
	}
	return false
}

// missingFromGroups names every key a named group lacks.
func (e ScalarSettingsEntry) missingFromGroups() config.Problems {
	var problems config.Problems
	for _, group := range settingsGroups {
		keys := group
		if group[0] == "network_proxy_enabled" {
			keys = networkGroupKeys
		}
		if !e.hasAny(keys) {
			continue
		}
		for _, key := range group {
			if !e.has(key) {
				problems = append(problems, config.Problem{Path: "settings." + key, Value: "(missing)",
					Expectation: "present: it goes with the other " + strings.SplitN(key, "_", 2)[0] +
						" keys; copy them together from an export"})
			}
		}
	}
	return problems
}

// CategoryEntry is one category and the roots attached to it.
type CategoryEntry struct {
	MediaType    string         `yaml:"media_type"`
	Name         string         `yaml:"name"`
	PriorityRank int64          `yaml:"priority_rank"`
	Roots        []AttachedRoot `yaml:"roots,omitempty"`
	// QualityProfile is the single profile of documents written before a library held a list;
	// read on import as a one-profile list, never written.
	QualityProfile string `yaml:"quality_profile,omitempty"`
	// QualityProfiles are the profiles the library accepts, by name, earlier preferred.
	QualityProfiles []string          `yaml:"quality_profiles,omitempty"`
	QualityCutoff   string            `yaml:"quality_cutoff,omitempty"`
	QualityTarget   *QualityTargetRef `yaml:"quality_target,omitempty"`
	// QualityUpgrades is written only when off, so documents from before it read as on.
	QualityUpgrades *bool `yaml:"quality_upgrades,omitempty"`
	// PreferredProtocol and ProtocolDelayMinutes are the delay profile; both are omitted at their defaults.
	PreferredProtocol    string `yaml:"preferred_protocol,omitempty"`
	ProtocolDelayMinutes int64  `yaml:"protocol_delay_minutes,omitempty"`
	LanguageProfile      string `yaml:"language_profile,omitempty"`
	// MonitorDiscovered is section 19.6's policy: what a scan does with an
	// album it finds that nobody asked for. Empty on import means the
	// column's own default (monitored) applies.
	MonitorDiscovered string `yaml:"monitor_discovered,omitempty"`
	// TVMonitor is what a scan monitors of a series it finds; written only when not the default (existing).
	TVMonitor string `yaml:"tv_monitor,omitempty"`
	// Kind is the library's kind within its media type; absent for none.
	Kind string `yaml:"kind,omitempty"`
	// Naming is absent for a library still on its media type's default.
	Naming *NamingEntry `yaml:"naming,omitempty"`
	// FileManagement is absent for a library still on Sonarr's defaults.
	FileManagement *FileManagementEntry `yaml:"file_management,omitempty"`
}

// FileManagementEntry is one library's File Management and Permissions.
type FileManagementEntry struct {
	PropersRepacks           PropersRepacks     `yaml:"propers_repacks"`
	RescanAfterRefresh       RescanAfterRefresh `yaml:"rescan_after_refresh"`
	ChangeFileDate           ChangeFileDate     `yaml:"change_file_date"`
	RecycleRetentionDays     int64              `yaml:"recycle_retention_days"`
	DeleteEmptyFolders       bool               `yaml:"delete_empty_folders"`
	CreateEmptyArtistFolders bool               `yaml:"create_empty_artist_folders"`
	UnmonitorDeleted         bool               `yaml:"unmonitor_deleted"`
	MergeArtistAliases       bool               `yaml:"merge_artist_aliases,omitempty"`
	SetPermissions           bool               `yaml:"set_permissions"`
	ChmodFolder              string             `yaml:"chmod_folder"`
	ChownGroup               string             `yaml:"chown_group,omitempty"`
	// WriteIDsToTags is optional so an export from before it existed keeps the default, on.
	WriteIDsToTags      *bool `yaml:"write_ids_to_tags,omitempty"`
	ReplaceIDsInTags    bool  `yaml:"replace_ids_in_tags,omitempty"`
	WriteMetadataToTags bool  `yaml:"write_metadata_to_tags,omitempty"`
	EmbedCoverInTags    bool  `yaml:"embed_cover_in_tags,omitempty"`
	NavidromeProfile    bool  `yaml:"navidrome_profile,omitempty"`
	// ImportExtraFiles and ExtraFileExtensions are absent while the library takes its media type's own.
	ImportExtraFiles    *bool    `yaml:"import_extra_files,omitempty"`
	ExtraFileExtensions []string `yaml:"extra_file_extensions,omitempty"`
	MediaServerFiles    bool     `yaml:"media_server_files,omitempty"`
}

func (e FileManagementEntry) fileManagement() FileManagement {
	return FileManagement{
		PropersRepacks: e.PropersRepacks, RescanAfterRefresh: e.RescanAfterRefresh, ChangeFileDate: e.ChangeFileDate,
		RecycleRetentionDays: e.RecycleRetentionDays, DeleteEmptyFolders: e.DeleteEmptyFolders,
		CreateEmptyArtistFolders: e.CreateEmptyArtistFolders, UnmonitorDeleted: e.UnmonitorDeleted,
		MergeArtistAliases: e.MergeArtistAliases,
		SetPermissions:     e.SetPermissions, ChmodFolder: e.ChmodFolder, ChownGroup: e.ChownGroup,
		Tagging: Tagging{
			WriteIDs: e.WriteIDsToTags == nil || *e.WriteIDsToTags, ReplaceIDs: e.ReplaceIDsInTags,
			WriteMetadata: e.WriteMetadataToTags, EmbedCover: e.EmbedCoverInTags, Navidrome: e.NavidromeProfile,
		},
		ExtraFiles:       ExtraFiles{Import: e.ImportExtraFiles, Extensions: e.ExtraFileExtensions},
		MediaServerFiles: e.MediaServerFiles,
	}
}

func fileManagementEntry(f FileManagement) FileManagementEntry {
	writeIDs := f.Tagging.WriteIDs
	return FileManagementEntry{
		PropersRepacks: f.PropersRepacks, RescanAfterRefresh: f.RescanAfterRefresh, ChangeFileDate: f.ChangeFileDate,
		RecycleRetentionDays: f.RecycleRetentionDays, DeleteEmptyFolders: f.DeleteEmptyFolders,
		CreateEmptyArtistFolders: f.CreateEmptyArtistFolders, UnmonitorDeleted: f.UnmonitorDeleted,
		MergeArtistAliases: f.MergeArtistAliases,
		SetPermissions:     f.SetPermissions, ChmodFolder: f.ChmodFolder, ChownGroup: f.ChownGroup, WriteIDsToTags: &writeIDs,
		ReplaceIDsInTags: f.Tagging.ReplaceIDs, WriteMetadataToTags: f.Tagging.WriteMetadata,
		EmbedCoverInTags: f.Tagging.EmbedCover, NavidromeProfile: f.Tagging.Navidrome,
		ImportExtraFiles: f.ExtraFiles.Import, ExtraFileExtensions: f.ExtraFiles.Extensions,
		MediaServerFiles: f.MediaServerFiles,
	}
}

// QualityTargetRef is a library's choice of target: the name of one under quality_targets. A document written before
// targets were named holds the target itself there, read on import as a target named after the library.
type QualityTargetRef struct {
	Name   string
	Inline *QualityTargetEntry
}

// MarshalYAML writes the name.
func (r QualityTargetRef) MarshalYAML() (any, error) { return r.Name, nil }

// UnmarshalYAML reads a name, or a legacy inline target.
func (r *QualityTargetRef) UnmarshalYAML(raw []byte) error {
	var name string
	if err := yaml.Unmarshal(raw, &name); err == nil {
		r.Name = name
		return nil
	}
	var inline QualityTargetEntry
	if err := yaml.Unmarshal(raw, &inline); err != nil {
		return fmt.Errorf("expected the name of a quality target: %w", err)
	}
	r.Inline = &inline
	return nil
}

// QualityTargetEntry is one named target a library may aim for.
type QualityTargetEntry struct {
	Name string `yaml:"name,omitempty"`
	// MediaType is omitted for music.
	MediaType    string `yaml:"media_type,omitempty"`
	AudioCodec   string `yaml:"audio_codec"`
	Container    string `yaml:"container"`
	BitrateKbps  *int64 `yaml:"bitrate_kbps,omitempty"`
	TolerancePct int64  `yaml:"tolerance_pct"`
	Resolution   string `yaml:"resolution,omitempty"`
	VideoCodec   string `yaml:"video_codec,omitempty"`
	HDR          string `yaml:"hdr,omitempty"`

	AudioChannels     string   `yaml:"audio_channels,omitempty"`
	AudioLanguages    []string `yaml:"audio_languages,omitempty"`
	SubtitleLanguages []string `yaml:"subtitle_languages,omitempty"`
	AudioBitrateKbps  *int64   `yaml:"audio_bitrate_kbps,omitempty"`
	VideoBitrateKbps  *int64   `yaml:"video_bitrate_kbps,omitempty"`
	FrameRate         float64  `yaml:"frame_rate,omitempty"`
	CRF               float64  `yaml:"crf,omitempty"`
}

// qualityProfileNames is the library's list, reading an older document's single profile as one.
func (e CategoryEntry) qualityProfileNames() []string {
	if len(e.QualityProfiles) == 0 && e.QualityProfile != "" {
		return []string{e.QualityProfile}
	}
	return e.QualityProfiles
}

// NamingEntry is a library's naming profile.
type NamingEntry struct {
	RenameFiles              bool              `yaml:"rename_files"`
	ReplaceIllegalCharacters bool              `yaml:"replace_illegal_characters"`
	ColonReplacement         string            `yaml:"colon_replacement"`
	Formats                  map[string]string `yaml:"formats"`
}

func (e NamingEntry) profile() NamingProfile {
	return NamingProfile(e)
}

// QualityProfileEntry is one ADR-40 profile. Rules and Upgrade stay opaque JSON
// text rather than nested YAML -- internal/identify/score, not settings, owns that shape,
// and settings must not need updating every time it grows a field.
type QualityProfileEntry struct {
	Name string `yaml:"name"`
	// MediaType is omitted for music, the only type exports before 2026-09-25 knew.
	MediaType string `yaml:"media_type,omitempty"`
	Rules     string `yaml:"rules"`
	Upgrade   string `yaml:"upgrade"`
}

// LanguageProfileEntry is one section 19.8 profile. Its fields are simple and
// fixed, so unlike QualityProfileEntry they round-trip as native YAML.
type LanguageProfileEntry struct {
	Name                string   `yaml:"name"`
	AudioPreferred      []string `yaml:"audio_preferred,omitempty"`
	AudioRequired       []string `yaml:"audio_required,omitempty"`
	SubtitlePreferred   []string `yaml:"subtitle_preferred,omitempty"`
	SubtitleRequired    []string `yaml:"subtitle_required,omitempty"`
	PreferOriginalAudio bool     `yaml:"prefer_original_audio,omitempty"`
	RejectDubbed        bool     `yaml:"reject_dubbed,omitempty"`
}

// AttachedRoot names a root by path, so the attachment survives the export.
type AttachedRoot struct {
	Path       string `yaml:"path"`
	Order      int64  `yaml:"order"`
	Weight     int64  `yaml:"weight"`
	AcceptsNew bool   `yaml:"accepts_new"`
}

// RootEntry is one library root. Its online state is left out on purpose: that
// is a fact about this machine at this moment, not configuration to carry.
type RootEntry struct {
	Path  string `yaml:"path"`
	Label string `yaml:"label,omitempty"`
}

// ExclusionPatternEntry is one ADR-53 part 1 pattern. Category is a natural
// key ("media_type/name", CategoryEntry's own shape), not an id -- ids are
// never portable across instances. Builtin patterns (shipped migration
// defaults) are never exported: every fresh install already gets them from
// its own migration, and CreateExclusionPattern has no way to mark a
// created row builtin, so re-importing an exported one would only ever
// create a second, non-builtin copy of it.
type ExclusionPatternEntry struct {
	Pattern   string `yaml:"pattern"`
	Action    string `yaml:"action"`
	MediaType string `yaml:"media_type,omitempty"`
	Category  string `yaml:"category,omitempty"`
	Note      string `yaml:"note,omitempty"`
	Enabled   bool   `yaml:"enabled"`
}

// AvailabilityGraceEntry is the wait after a part's time, in hours.
type AvailabilityGraceEntry struct {
	High   int64 `yaml:"high"`
	Medium int64 `yaml:"medium"`
	Low    int64 `yaml:"low"`
}

// DocumentVersion is the document's shape, not the product's version.
const DocumentVersion = 1

// Export renders the current configuration, ordered by natural key so the file
// is stable in version control.
func (c *Cache) Export() Document {
	snapshot := c.Snapshot()

	network := snapshot.NetworkSettings()
	cacheBudget := snapshot.CacheBudget()
	priority := snapshot.PrioritySettings()
	metadata := snapshot.MetadataSettings()
	recycleBin := snapshot.RecycleBinLimits()
	settingsEntry := ScalarSettingsEntry{
		AuthRequired:                 snapshot.AuthRequired(),
		ImportAutoCommitConfidence:   snapshot.ImportAutoCommitConfidence(),
		ImportTransferLadder:         snapshot.ImportTransferLadder(),
		ImportRenameOnImport:         snapshot.ImportRenameOnImport(),
		NetworkPort:                  network.Port,
		NetworkProxyEnabled:          network.ProxyEnabled,
		NetworkProxyURL:              network.ProxyURL,
		NetworkProxyBypass:           network.ProxyBypass,
		NetworkAllowCIDRsEnabled:     network.AllowCIDRsEnabled,
		NetworkAllowCIDRs:            network.AllowCIDRs,
		RecycleBinMaxBytes:           &recycleBin.MaxBytes,
		RecycleBinMaxPercentOfRoot:   &recycleBin.MaxPercentOfRoot,
		CacheMaxBytes:                cacheBudget.MaxBytes,
		CacheMaxAgeDays:              cacheBudget.MaxAgeDays,
		CacheImagesSharePercent:      cacheBudget.ImagesSharePercent,
		CacheMetadataSharePercent:    cacheBudget.MetadataSharePercent,
		PriorityUserRequestWeight:    priority.UserRequestWeight,
		PriorityNewReleaseWeight:     priority.NewReleaseWeight,
		PriorityGapWeight:            priority.GapWeight,
		PriorityUpgradeWeight:        priority.UpgradeWeight,
		PriorityNewReleaseDays:       priority.NewReleaseDays,
		AutomaticSearchesPerHour:     priority.SearchesPerHour,
		RSSFreshHours:                priority.RSSFreshHours,
		BrainzMashUserAgent:          snapshot.MetadataSettings().BrainzMashUserAgent,
		MetadataSourceOrder:          snapshot.MetadataSettings().SourceOrder,
		MetadataCoverSources:         &metadata.CoverSources,
		MetadataTVSources:            &metadata.TVSources,
		MetadataWikidata:             metadata.Wikidata,
		UsersNewLibraryPermission:    snapshot.NewLibraryPermission(),
		BackupRetention:              snapshot.BackupRetention().Mode,
		BackupKeepCount:              snapshot.BackupRetention().KeepCount,
		BackupKeepDays:               snapshot.BackupRetention().KeepDays,
		MetadataAcoustID:             metadata.AcoustID,
		MetadataTheAudioDB:           metadata.TheAudioDB,
		MetadataPeopleBackfillPerRun: metadata.PeopleBackfillPerRun,
		AvailabilityGraceHours: &AvailabilityGraceEntry{
			High: metadata.AvailabilityGrace.High, Medium: metadata.AvailabilityGrace.Medium, Low: metadata.AvailabilityGrace.Low,
		},
	}

	roots := snapshot.RootFolders()
	pathOf := make(map[int64]string, len(roots))
	document := Document{Version: DocumentVersion, Settings: &settingsEntry, Schedules: snapshot.scheduleOverrides()}
	for _, root := range roots {
		pathOf[root.ID] = root.Path
		document.Roots = append(document.Roots, RootEntry{Path: root.Path, Label: root.Label})
	}
	// Redundant while ListRootFolders orders by path; kept so the ordering does
	// not rest on a query written for the interface.
	sort.Slice(document.Roots, func(i, j int) bool {
		return document.Roots[i].Path < document.Roots[j].Path
	})

	qualityNameByID := make(map[int64]string, len(snapshot.QualityProfiles()))
	for _, profile := range snapshot.QualityProfiles() {
		qualityNameByID[profile.ID] = profile.Name
		document.QualityProfiles = append(document.QualityProfiles, QualityProfileEntry{
			Name: profile.Name, Rules: profile.RulesJSON, Upgrade: profile.UpgradeJSON,
			MediaType: exportedMediaType(profile.MediaType),
		})
	}
	sort.Slice(document.QualityProfiles, func(i, j int) bool {
		return document.QualityProfiles[i].Name < document.QualityProfiles[j].Name
	})

	for _, target := range snapshot.QualityTargets() {
		document.QualityTargets = append(document.QualityTargets, target.entry())
	}

	languageNameByID := make(map[int64]string, len(snapshot.LanguageProfiles()))
	for _, profile := range snapshot.LanguageProfiles() {
		languageNameByID[profile.ID] = profile.Name
		document.LanguageProfiles = append(document.LanguageProfiles, LanguageProfileEntry{
			Name: profile.Name, AudioPreferred: profile.AudioPreferred, AudioRequired: profile.AudioRequired,
			SubtitlePreferred: profile.SubtitlePreferred, SubtitleRequired: profile.SubtitleRequired,
			PreferOriginalAudio: profile.PreferOriginalAudio, RejectDubbed: profile.RejectDubbed,
		})
	}
	sort.Slice(document.LanguageProfiles, func(i, j int) bool {
		return document.LanguageProfiles[i].Name < document.LanguageProfiles[j].Name
	})

	attachments := snapshot.CategoryRoots()
	for _, category := range snapshot.Categories() {
		entry := CategoryEntry{
			MediaType:         category.MediaType,
			Name:              category.Name,
			PriorityRank:      category.PriorityRank,
			MonitorDiscovered: category.MonitorDiscovered,
		}
		entry.Kind = category.Kind
		if category.TVMonitor != TVMonitorExisting {
			entry.TVMonitor = category.TVMonitor
		}
		for _, id := range category.QualityProfileIDs {
			entry.QualityProfiles = append(entry.QualityProfiles, qualityNameByID[id])
		}
		if category.QualityCutoffID != nil {
			entry.QualityCutoff = qualityNameByID[*category.QualityCutoffID]
		}
		if target := category.QualityTarget; target != nil {
			entry.QualityTarget = &QualityTargetRef{Name: target.Name}
		}
		if !category.QualityUpgrades {
			off := false
			entry.QualityUpgrades = &off
		}
		entry.PreferredProtocol, entry.ProtocolDelayMinutes = category.PreferredProtocol, category.ProtocolDelayMinutes
		if category.LanguageProfileID != nil {
			entry.LanguageProfile = languageNameByID[*category.LanguageProfileID]
		}
		if category.Naming != nil {
			entry.Naming = &NamingEntry{
				RenameFiles: category.Naming.RenameFiles, ReplaceIllegalCharacters: category.Naming.ReplaceIllegalCharacters,
				ColonReplacement: category.Naming.ColonReplacement, Formats: category.Naming.Formats,
			}
		}
		if !category.FileManagement.IsDefault() {
			entry.FileManagement = &FileManagementEntry{}
			*entry.FileManagement = fileManagementEntry(category.FileManagement)
		}
		for _, attachment := range attachments {
			if attachment.CategoryID != category.ID {
				continue
			}
			path, known := pathOf[attachment.RootFolderID]
			if !known {
				// Not exportable as a path; a data problem, not the exporter's.
				continue
			}
			entry.Roots = append(entry.Roots, AttachedRoot{
				Path: path, Order: attachment.Order,
				Weight: attachment.Weight, AcceptsNew: attachment.AcceptsNew,
			})
		}
		sort.Slice(entry.Roots, func(i, j int) bool { return entry.Roots[i].Path < entry.Roots[j].Path })
		document.Categories = append(document.Categories, entry)
	}
	sort.Slice(document.Categories, func(i, j int) bool {
		left, right := document.Categories[i], document.Categories[j]
		if left.MediaType != right.MediaType {
			return left.MediaType < right.MediaType
		}
		return left.Name < right.Name
	})

	categoryKeyByID := make(map[int64]string, len(snapshot.Categories()))
	for _, category := range snapshot.Categories() {
		categoryKeyByID[category.ID] = category.MediaType + "/" + category.Name
	}
	for _, pattern := range snapshot.ExclusionPatterns() {
		if pattern.Builtin {
			continue
		}
		entry := ExclusionPatternEntry{
			Pattern: pattern.Pattern, Action: pattern.Action, MediaType: pattern.MediaType,
			Note: pattern.Note, Enabled: pattern.Enabled,
		}
		if pattern.CategoryID != nil {
			entry.Category = categoryKeyByID[*pattern.CategoryID]
		}
		document.ExclusionPatterns = append(document.ExclusionPatterns, entry)
	}
	sort.Slice(document.ExclusionPatterns, func(i, j int) bool {
		left, right := document.ExclusionPatterns[i], document.ExclusionPatterns[j]
		if left.Pattern != right.Pattern {
			return left.Pattern < right.Pattern
		}
		return left.Category < right.Category
	})

	return document
}

// prefixed namespaces a problem's own path under a document section, the
// same "where + \".\" + problem.Path" rewrite every section below already
// did individually.
func prefixed(section string, problem config.Problem) config.Problem {
	problem.Path = section + "." + problem.Path
	return problem
}

// Validate reports every problem with a document, not the first (section
// 14.4). callerAddress is the same "must not lock itself out" check
// SetNetworkSettings applies (Invariant 89) -- an invalid address (the zero
// value, e.g. from a caller with no real one to give) can never satisfy a
// non-empty allow_cidrs, refusing conservatively rather than risking a
// lockout it cannot verify against.
func (d Document) Validate(callerAddress netip.Addr) config.Problems {
	var problems config.Problems
	problems = append(problems, validateScheduleEntries(d.Schedules)...)

	if d.Settings != nil {
		problems = append(problems, d.Settings.missingFromGroups()...)
		if d.Settings.has("import_auto_commit_confidence") {
			for _, problem := range validateImportAutoCommitConfidence(d.Settings.ImportAutoCommitConfidence) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		// Absent means "say nothing about it", unlike the API setter where an
		// empty ladder is a user clearing every method and is refused.
		if len(d.Settings.ImportTransferLadder) > 0 {
			for _, problem := range validateImportTransferLadder(d.Settings.ImportTransferLadder) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		network := NetworkSettings{
			Port: d.Settings.NetworkPort, ProxyEnabled: d.Settings.NetworkProxyEnabled,
			ProxyURL: d.Settings.NetworkProxyURL, ProxyBypass: d.Settings.NetworkProxyBypass,
			AllowCIDRsEnabled: d.Settings.NetworkAllowCIDRsEnabled, AllowCIDRs: d.Settings.NetworkAllowCIDRs,
		}
		if d.Settings.hasAny(networkGroupKeys) && d.Settings.hasAll(settingsGroups[0]) {
			for _, problem := range validateNetworkSettings(network, callerAddress) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		cacheBudget := CacheBudget{
			MaxBytes: d.Settings.CacheMaxBytes, MaxAgeDays: d.Settings.CacheMaxAgeDays,
			ImagesSharePercent: d.Settings.CacheImagesSharePercent, MetadataSharePercent: d.Settings.CacheMetadataSharePercent,
		}
		if limits := d.Settings.recycleBinLimits(); limits != nil {
			for _, problem := range validateRecycleBinLimits(*limits) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if d.Settings.hasAll(settingsGroups[1]) {
			for _, problem := range validateCacheBudget(cacheBudget) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		priority := PrioritySettings{
			UserRequestWeight: d.Settings.PriorityUserRequestWeight, NewReleaseWeight: d.Settings.PriorityNewReleaseWeight,
			GapWeight: d.Settings.PriorityGapWeight, UpgradeWeight: d.Settings.PriorityUpgradeWeight,
			NewReleaseDays: d.Settings.PriorityNewReleaseDays,
		}
		if d.Settings.hasAll(settingsGroups[2]) {
			for _, problem := range validatePrioritySettings(priority) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if hours := d.Settings.RSSFreshHours; hours != nil {
			for _, problem := range validatePrioritySettings(PrioritySettings{
				UserRequestWeight: 0, NewReleaseDays: 1, RSSFreshHours: hours}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if perHour := d.Settings.AutomaticSearchesPerHour; perHour != nil {
			for _, problem := range validatePrioritySettings(PrioritySettings{
				UserRequestWeight: 0, NewReleaseDays: 1, SearchesPerHour: perHour}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		// Absent means the document predates the setting, so the stored agent is kept.
		if d.Settings.BrainzMashUserAgent != "" {
			// Absent means "say nothing about it", so the order validated here
			// is the document's own when it has one and the default otherwise.
			order := d.Settings.MetadataSourceOrder
			if len(order) == 0 {
				order = DefaultMetadataSourceOrder
			}
			for _, problem := range validateMetadataSettings(MetadataSettings{
				BrainzMashUserAgent: d.Settings.BrainzMashUserAgent, SourceOrder: order}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if grace := d.Settings.AvailabilityGraceHours; grace != nil {
			for _, problem := range validateMetadataSettings(MetadataSettings{BrainzMashUserAgent: DefaultBrainzMashUserAgent,
				AvailabilityGrace: &AvailabilityGraceHours{High: grace.High, Medium: grace.Medium, Low: grace.Low}}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if perRun := d.Settings.MetadataPeopleBackfillPerRun; perRun != nil {
			for _, problem := range validateMetadataSettings(MetadataSettings{
				BrainzMashUserAgent: DefaultBrainzMashUserAgent, PeopleBackfillPerRun: perRun}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if covers := d.Settings.MetadataCoverSources; covers != nil {
			for _, problem := range validateMetadataSettings(MetadataSettings{
				BrainzMashUserAgent: DefaultBrainzMashUserAgent, CoverSources: *covers}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if sources := d.Settings.MetadataTVSources; sources != nil {
			for _, problem := range validateMetadataSettings(MetadataSettings{
				BrainzMashUserAgent: DefaultBrainzMashUserAgent, TVSources: *sources}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if d.Settings.BackupRetention != "" {
			for _, problem := range validateBackupRetention(BackupRetention{Mode: d.Settings.BackupRetention,
				KeepCount: d.Settings.BackupKeepCount, KeepDays: d.Settings.BackupKeepDays}) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		if permission := d.Settings.UsersNewLibraryPermission; permission != "" && !permission.valid() {
			problems = append(problems, config.Problem{Path: "settings.users_new_library_permission",
				Value: string(permission), Expectation: "none, read, request or manage"})
		}
	}

	if d.Version == 0 {
		problems = append(problems, config.Problem{
			Path: "version", Expectation: fmt.Sprintf("a document version; this build reads %d", DocumentVersion),
		})
	} else if d.Version > DocumentVersion {
		problems = append(problems, config.Problem{
			Path:  "version",
			Value: fmt.Sprint(d.Version),
			Expectation: fmt.Sprintf(
				"a version this build understands (%d or lower); upgrade Solarr to read it",
				DocumentVersion),
		})
	}

	knownPaths := make(map[string]bool, len(d.Roots))
	for index, root := range d.Roots {
		where := fmt.Sprintf("root_folders[%d]", index)
		for _, problem := range validateRootFolder(root.Path) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}

		switch {
		case knownPaths[root.Path]:
			problems = append(problems, config.Problem{
				Path: where + ".path", Value: root.Path, Expectation: "a path listed once",
			})
		default:
			knownPaths[root.Path] = true
		}
	}

	knownQualityProfiles := make(map[string]bool, len(d.QualityProfiles))
	for index, profile := range d.QualityProfiles {
		where := fmt.Sprintf("quality_profiles[%d]", index)
		for _, problem := range validateQualityProfile(profile.Name, profile.Rules, profile.Upgrade) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}
		if knownQualityProfiles[profile.Name] {
			problems = append(problems, config.Problem{
				Path: where, Value: profile.Name, Expectation: "a quality profile listed once",
			})
		}
		knownQualityProfiles[profile.Name] = true
	}

	knownQualityTargets := map[string]bool{}
	for index, target := range d.QualityTargets {
		where := fmt.Sprintf("quality_targets[%d]", index)
		entry := target.target()
		if entry.MediaType == "" {
			entry.MediaType = "music"
		}
		key := entry.MediaType + "\x00" + entry.Name
		for _, problem := range validateQualityTargetNamed(&entry) {
			problem.Path = where + "." + strings.TrimPrefix(problem.Path, "target.")
			problems = append(problems, problem)
		}
		if knownQualityTargets[key] {
			problems = append(problems, config.Problem{Path: where, Value: entry.Name, Expectation: "a quality target listed once"})
		}
		knownQualityTargets[key] = true
	}

	knownLanguageProfiles := make(map[string]bool, len(d.LanguageProfiles))
	for index, profile := range d.LanguageProfiles {
		where := fmt.Sprintf("language_profiles[%d]", index)
		for _, problem := range validateLanguageProfile(profile.Name) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}
		if knownLanguageProfiles[profile.Name] {
			problems = append(problems, config.Problem{
				Path: where, Value: profile.Name, Expectation: "a language profile listed once",
			})
		}
		knownLanguageProfiles[profile.Name] = true
	}

	seenCategories := make(map[string]bool, len(d.Categories))
	rootOwners := make(map[string]string)
	for index, category := range d.Categories {
		where := fmt.Sprintf("categories[%d]", index)
		// The same rule the API path applies, so an import cannot introduce a
		// category that could not be created through the interface.
		for _, problem := range validateCategory(category.MediaType, category.Name, category.PriorityRank) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}

		key := category.MediaType + "/" + category.Name
		if seenCategories[key] {
			problems = append(problems, config.Problem{
				Path: where, Value: key, Expectation: "a category listed once",
			})
		}
		seenCategories[key] = true

		if category.FileManagement != nil {
			for _, problem := range validateFileManagement(category.FileManagement.fileManagement()) {
				problem.Path = where + ".file_management." + problem.Path
				problems = append(problems, problem)
			}
		}

		for rootIndex, attached := range category.Roots {
			// Or the category imports with a silently missing disc.
			if !knownPaths[attached.Path] {
				problems = append(problems, config.Problem{
					Path:        fmt.Sprintf("%s.roots[%d].path", where, rootIndex),
					Value:       attached.Path,
					Expectation: "a path also listed under root_folders",
				})
			}
			if owner, taken := rootOwners[attached.Path]; taken && owner != key {
				problems = append(problems, config.Problem{
					Path:        fmt.Sprintf("%s.roots[%d].path", where, rootIndex),
					Value:       attached.Path,
					Expectation: "a root folder in at most one library (also listed under " + owner + ")",
				})
			}
			rootOwners[attached.Path] = key
		}
		names := category.qualityProfileNames()
		for _, name := range names {
			if !knownQualityProfiles[name] {
				problems = append(problems, config.Problem{
					Path: where + ".quality_profiles", Value: name,
					Expectation: "a name also listed under quality_profiles",
				})
			}
		}
		if category.QualityCutoff != "" && !slices.Contains(names, category.QualityCutoff) {
			problems = append(problems, config.Problem{
				Path: where + ".quality_cutoff", Value: category.QualityCutoff,
				Expectation: "one of the category's own quality_profiles",
			})
		}
		if target := category.QualityTarget; target != nil {
			if target.Inline != nil {
				entry := target.Inline.target()
				for _, problem := range validateQualityTarget(&entry) {
					problem.Path = where + ".quality_" + strings.TrimPrefix(problem.Path, "target.")
					problems = append(problems, problem)
				}
			} else if !knownQualityTargets[category.MediaType+"\x00"+target.Name] {
				problems = append(problems, config.Problem{
					Path: where + ".quality_target", Value: target.Name,
					Expectation: "a name also listed under quality_targets",
				})
			}
		}
		if category.LanguageProfile != "" && !knownLanguageProfiles[category.LanguageProfile] {
			problems = append(problems, config.Problem{
				Path: where + ".language_profile", Value: category.LanguageProfile,
				Expectation: "a name also listed under language_profiles",
			})
		}
		if category.MonitorDiscovered != "" {
			problems = append(problems, validateMonitorDiscovered(category.MonitorDiscovered)...)
		}
		if category.TVMonitor != "" {
			problems = append(problems, validateTVMonitor(category.TVMonitor)...)
		}
		problems = append(problems, validateKind(category.Kind)...)
	}

	for index, pattern := range d.ExclusionPatterns {
		where := fmt.Sprintf("exclusion_patterns[%d]", index)
		// categoryExists always true here: category existence is checked
		// against this same document below, by name, the same self-
		// consistency rule quality_profile/language_profile already apply
		// on a category entry -- an id has no meaning until Import resolves
		// one, and this function never touches the database (Validate is a
		// pure function of the document, same doc comment as every other
		// validator here).
		for _, problem := range validateExclusionPattern(
			pattern.Pattern, pattern.Action, pattern.MediaType, nil, func(int64) bool { return true },
		) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}
		if pattern.Category != "" && !seenCategories[pattern.Category] {
			problems = append(problems, config.Problem{
				Path: where + ".category", Value: pattern.Category,
				Expectation: "a media_type/name also listed under categories",
			})
		}
	}

	return problems
}

// Import applies a document in one transaction, or changes nothing. Merges on
// natural keys. See documentation/configuration-export.md. callerAddress is
// Document.Validate's own Invariant 89 check -- the real remote address for
// an HTTP-driven import (ImportSettings), the zero value for anything else,
// which conservatively refuses a document setting a non-empty allow_cidrs
// rather than risk a lockout it cannot verify.
func (c *Cache) Import(ctx context.Context, cl clock.Clock, callerAddress netip.Addr, document Document) error {
	problems := document.Validate(callerAddress)
	if c.validateNaming != nil {
		for index, category := range document.Categories {
			if category.Naming == nil {
				continue
			}
			if err := c.validateNaming(category.MediaType, category.Naming.profile()); err != nil {
				problems = append(problems, config.Problem{
					Path: fmt.Sprintf("categories[%d].naming", index), Value: category.MediaType + "/" + category.Name,
					Expectation: err.Error(),
				})
			}
		}
	}
	if len(problems) > 0 {
		return problems
	}

	administrators := c.administratorIDs()
	snapshot := c.Snapshot()

	rootIDByPath := make(map[string]int64)
	for _, root := range snapshot.RootFolders() {
		rootIDByPath[root.Path] = root.ID
	}
	categoryIDByKey := make(map[string]int64)
	categoryKeyByID := make(map[int64]string, len(snapshot.Categories()))
	for _, category := range snapshot.Categories() {
		key := category.MediaType + "/" + category.Name
		categoryIDByKey[key] = category.ID
		categoryKeyByID[category.ID] = key
	}
	// The same "create only if this exact combination is not already
	// present" idempotency every other section here has, keyed on every
	// field a pattern actually is rather than a single name (it has none).
	existingPatternKeys := make(map[string]bool, len(snapshot.ExclusionPatterns()))
	for _, pattern := range snapshot.ExclusionPatterns() {
		category := ""
		if pattern.CategoryID != nil {
			category = categoryKeyByID[*pattern.CategoryID]
		}
		existingPatternKeys[exclusionPatternKey(pattern.Pattern, pattern.Action, pattern.MediaType, category)] = true
	}
	qualityIDByName := make(map[string]int64)
	for _, profile := range snapshot.QualityProfiles() {
		qualityIDByName[profile.Name] = profile.ID
	}
	targetIDByKey := make(map[string]int64)
	for _, target := range snapshot.QualityTargets() {
		targetIDByKey[target.MediaType+"\x00"+target.Name] = target.ID
	}
	languageIDByName := make(map[string]int64)
	for _, profile := range snapshot.LanguageProfiles() {
		languageIDByName[profile.Name] = profile.ID
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if document.Settings != nil {
			if err := importScalarSettings(ctx, queries, cl, *document.Settings); err != nil {
				return err
			}
		}
		if err := importSchedules(ctx, queries, cl, document.Schedules); err != nil {
			return err
		}

		for _, profile := range document.QualityProfiles {
			if _, exists := qualityIDByName[profile.Name]; exists {
				continue
			}
			mediaType := profile.MediaType
			if mediaType == "" {
				mediaType = "music"
			}
			if !slices.Contains(MediaTypes, mediaType) {
				return fmt.Errorf("quality profile %q: media_type %q is not one of %s", profile.Name, mediaType, strings.Join(MediaTypes, ", "))
			}
			created, err := queries.CreateQualityProfile(ctx, settingsdb.CreateQualityProfileParams{
				Name: profile.Name, RulesJson: profile.Rules, UpgradeJson: profile.Upgrade, MediaType: mediaType,
			})
			if err != nil {
				return fmt.Errorf("quality profile %q: %w", profile.Name, err)
			}
			qualityIDByName[profile.Name] = created.ID
		}

		for _, target := range document.QualityTargets {
			entry := target.target()
			if entry.MediaType == "" {
				entry.MediaType = "music"
			}
			key := entry.MediaType + "\x00" + entry.Name
			if _, exists := targetIDByKey[key]; exists {
				continue
			}
			id, err := queries.CreateQualityTarget(ctx, entry.createParams())
			if err != nil {
				return fmt.Errorf("quality target %q: %w", entry.Name, err)
			}
			targetIDByKey[key] = id
		}

		for _, profile := range document.LanguageProfiles {
			if _, exists := languageIDByName[profile.Name]; exists {
				continue
			}
			audioPreferred, err := encodeStringArray(profile.AudioPreferred)
			if err != nil {
				return fmt.Errorf("language profile %q: %w", profile.Name, err)
			}
			audioRequired, err := encodeOptionalStringArray(profile.AudioRequired)
			if err != nil {
				return fmt.Errorf("language profile %q: %w", profile.Name, err)
			}
			subtitlePreferred, err := encodeOptionalStringArray(profile.SubtitlePreferred)
			if err != nil {
				return fmt.Errorf("language profile %q: %w", profile.Name, err)
			}
			subtitleRequired, err := encodeOptionalStringArray(profile.SubtitleRequired)
			if err != nil {
				return fmt.Errorf("language profile %q: %w", profile.Name, err)
			}
			created, err := queries.CreateLanguageProfile(ctx, settingsdb.CreateLanguageProfileParams{
				Name:                  profile.Name,
				AudioPreferredJson:    audioPreferred,
				AudioRequiredJson:     audioRequired,
				SubtitlePreferredJson: subtitlePreferred,
				SubtitleRequiredJson:  subtitleRequired,
				PreferOriginalAudio:   boolToInt(profile.PreferOriginalAudio),
				RejectDubbed:          boolToInt(profile.RejectDubbed),
			})
			if err != nil {
				return fmt.Errorf("language profile %q: %w", profile.Name, err)
			}
			languageIDByName[profile.Name] = created.ID
		}

		for _, root := range document.Roots {
			if _, exists := rootIDByPath[root.Path]; exists {
				continue
			}
			created, err := queries.CreateRootFolder(ctx, settingsdb.CreateRootFolderParams{
				Path:         root.Path,
				Label:        sql.NullString{String: root.Label, Valid: root.Label != ""},
				Online:       1,
				MinFreeBytes: DefaultMinFreeBytes,
			})
			if err != nil {
				return fmt.Errorf("root folder %q: %w", root.Path, err)
			}
			rootIDByPath[root.Path] = created.ID
		}

		for _, category := range document.Categories {
			key := category.MediaType + "/" + category.Name
			id, exists := categoryIDByKey[key]
			if !exists {
				created, err := queries.CreateCategory(ctx, settingsdb.CreateCategoryParams{
					MediaType: category.MediaType, Name: category.Name,
					PriorityRank: category.PriorityRank,
				})
				if err != nil {
					return fmt.Errorf("category %q: %w", key, err)
				}
				id = created.ID
				categoryIDByKey[key] = id

				// As CreateCategory does: explicit and visible (ADR-27).
				for _, administrator := range administrators {
					if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
						UserID: administrator, CategoryID: id, Permission: string(PermissionManage),
					}); err != nil {
						return fmt.Errorf("category %q access: %w", key, err)
					}
				}
			}

			for _, attached := range category.Roots {
				if err := queries.AttachRootFolderToCategory(ctx,
					settingsdb.AttachRootFolderToCategoryParams{
						CategoryID: id, RootFolderID: rootIDByPath[attached.Path],
						Ord: attached.Order, Weight: attached.Weight,
						AcceptsNew: boolToInt(attached.AcceptsNew),
					}); err != nil {
					return fmt.Errorf("category %q root %q: %w", key, attached.Path, err)
				}
			}

			if names := category.qualityProfileNames(); len(names) > 0 {
				ids := make([]int64, len(names))
				for i, name := range names {
					ids[i] = qualityIDByName[name]
				}
				var cutoff *int64
				if category.QualityCutoff != "" {
					id := qualityIDByName[category.QualityCutoff]
					cutoff = &id
				}
				if err := writeQualityList(ctx, queries, id, ids, cutoff); err != nil {
					return fmt.Errorf("category %q quality profiles: %w", key, err)
				}
			}
			if target := category.QualityTarget; target != nil {
				name := target.Name
				if target.Inline != nil {
					legacy := target.Inline.target()
					legacy.Name, legacy.MediaType = category.Name+" target", category.MediaType
					name = legacy.Name
					if _, exists := targetIDByKey[legacy.MediaType+"\x00"+name]; !exists {
						created, err := queries.CreateQualityTarget(ctx, legacy.createParams())
						if err != nil {
							return fmt.Errorf("category %q quality target: %w", key, err)
						}
						targetIDByKey[legacy.MediaType+"\x00"+name] = created
					}
				}
				if err := queries.SetCategoryTarget(ctx, settingsdb.SetCategoryTargetParams{
					CategoryID: id, TargetID: targetIDByKey[category.MediaType+"\x00"+name],
				}); err != nil {
					return fmt.Errorf("category %q quality target: %w", key, err)
				}
			}
			if category.QualityUpgrades != nil && !*category.QualityUpgrades {
				if err := queries.SetCategoryQualityUpgrades(ctx, settingsdb.SetCategoryQualityUpgradesParams{ID: id}); err != nil {
					return fmt.Errorf("category %q quality upgrades: %w", key, err)
				}
			}
			if category.PreferredProtocol != "" || category.ProtocolDelayMinutes != 0 {
				if err := queries.SetCategoryProtocolDelay(ctx, settingsdb.SetCategoryProtocolDelayParams{
					PreferredProtocol: category.PreferredProtocol, ProtocolDelayMinutes: category.ProtocolDelayMinutes, ID: id,
				}); err != nil {
					return fmt.Errorf("category %q protocol delay: %w", key, err)
				}
			}
			if category.MonitorDiscovered != "" {
				if err := queries.SetCategoryMonitorDiscovered(ctx, settingsdb.SetCategoryMonitorDiscoveredParams{
					MonitorDiscovered: category.MonitorDiscovered, ID: id,
				}); err != nil {
					return fmt.Errorf("category %q monitor_discovered: %w", key, err)
				}
			}
			if category.Kind != "" {
				if err := queries.SetCategoryKind(ctx, settingsdb.SetCategoryKindParams{Kind: category.Kind, ID: id}); err != nil {
					return fmt.Errorf("category %q kind: %w", key, err)
				}
			}
			if category.TVMonitor != "" {
				if err := queries.SetCategoryTVMonitor(ctx, settingsdb.SetCategoryTVMonitorParams{TvMonitor: category.TVMonitor, ID: id}); err != nil {
					return fmt.Errorf("category %q tv_monitor: %w", key, err)
				}
			}
			if category.LanguageProfile != "" {
				if err := queries.SetCategoryLanguageProfile(ctx, settingsdb.SetCategoryLanguageProfileParams{
					LanguageProfileID: sql.NullInt64{Int64: languageIDByName[category.LanguageProfile], Valid: true},
					ID:                id,
				}); err != nil {
					return fmt.Errorf("category %q language profile: %w", key, err)
				}
			}
			if category.Naming != nil {
				if err := writeCategoryNaming(ctx, queries, id, category.Naming.profile()); err != nil {
					return fmt.Errorf("category %q naming: %w", key, err)
				}
			}
			if category.FileManagement != nil {
				if err := writeCategoryFileManagement(ctx, queries, id, category.FileManagement.fileManagement()); err != nil {
					return fmt.Errorf("category %q file management: %w", key, err)
				}
			}
		}

		for _, pattern := range document.ExclusionPatterns {
			key := exclusionPatternKey(pattern.Pattern, pattern.Action, pattern.MediaType, pattern.Category)
			if existingPatternKeys[key] {
				continue
			}
			var categoryID sql.NullInt64
			if pattern.Category != "" {
				categoryID = sql.NullInt64{Int64: categoryIDByKey[pattern.Category], Valid: true}
			}
			if _, err := queries.CreateExclusionPattern(ctx, settingsdb.CreateExclusionPatternParams{
				Pattern: pattern.Pattern, Action: pattern.Action,
				MediaType:  sql.NullString{String: pattern.MediaType, Valid: pattern.MediaType != ""},
				CategoryID: categoryID, Note: pattern.Note,
				Builtin: 0, Enabled: boolToInt(pattern.Enabled),
			}); err != nil {
				return fmt.Errorf("exclusion pattern %q: %w", pattern.Pattern, err)
			}
			existingPatternKeys[key] = true
		}
		return nil
	})
}

// exclusionPatternKey is an exclusion pattern's own identity for Import's
// idempotency check: it has no single natural-key field the way a profile
// has Name, so every field that distinguishes one pattern from another goes
// into the key instead.
func exclusionPatternKey(pattern, action, mediaType, category string) string {
	return pattern + "\x00" + action + "\x00" + mediaType + "\x00" + category
}

// importScalarSettings writes every todo-4.14-registered scalar setting
// straight through SetInstanceSetting, inside Import's own transaction --
// not through the typed Cache.Set* wrappers (SetNetworkSettings and
// friends), each of which is its own c.write (validate, commit, reload,
// maybe back up): calling five of those from inside one already-open write
// would nest transactions. Validation already happened in Document.Validate
// before this runs.
func importScalarSettings(ctx context.Context, queries *settingsdb.Queries, cl clock.Clock, entry ScalarSettingsEntry) error {
	proxyBypass, err := json.Marshal(entry.NetworkProxyBypass)
	if err != nil {
		return fmt.Errorf("settings.network_proxy_bypass: %w", err)
	}
	allowCIDRs, err := json.Marshal(entry.NetworkAllowCIDRs)
	if err != nil {
		return fmt.Errorf("settings.network_allow_cidrs: %w", err)
	}

	ladder := entry.ImportTransferLadder
	if len(ladder) == 0 {
		ladder = DefaultImportTransferLadder
	}
	transferLadder, err := json.Marshal(ladder)
	if err != nil {
		return fmt.Errorf("settings.import_transfer_ladder: %w", err)
	}

	values := map[string]string{}
	if entry.has("auth_required") {
		values[KeyAuthRequired] = strconv.FormatBool(entry.AuthRequired)
	}
	if entry.present == nil || len(entry.ImportTransferLadder) > 0 {
		values[KeyImportTransferLadder] = string(transferLadder)
	}
	if entry.has("import_auto_commit_confidence") {
		values[KeyImportAutoCommitConfidence] = strconv.FormatFloat(entry.ImportAutoCommitConfidence, 'f', -1, 64)
	}
	if entry.has("import_rename_on_import") {
		values[KeyImportRenameOnImport] = strconv.FormatBool(entry.ImportRenameOnImport)
	}
	if entry.hasAny(networkGroupKeys) {
		values[KeyNetworkPort] = strconv.Itoa(entry.NetworkPort)
		values[KeyNetworkProxyEnabled] = strconv.FormatBool(entry.NetworkProxyEnabled)
		values[KeyNetworkProxyURL] = entry.NetworkProxyURL
		values[KeyNetworkProxyBypass] = string(proxyBypass)
		values[KeyNetworkAllowCIDRsEnabled] = strconv.FormatBool(entry.NetworkAllowCIDRsEnabled)
		values[KeyNetworkAllowCIDRs] = string(allowCIDRs)
	}
	if entry.hasAny(settingsGroups[1]) {
		values[KeyCacheMaxBytes] = strconv.FormatInt(entry.CacheMaxBytes, 10)
		values[KeyCacheMaxAgeDays] = strconv.FormatInt(entry.CacheMaxAgeDays, 10)
		values[KeyCacheImagesShare] = strconv.FormatInt(entry.CacheImagesSharePercent, 10)
		values[KeyCacheMetadataShare] = strconv.FormatInt(entry.CacheMetadataSharePercent, 10)
	}
	if entry.hasAny(settingsGroups[2]) {
		values[KeyPriorityUserRequestWeight] = strconv.FormatInt(entry.PriorityUserRequestWeight, 10)
		values[KeyPriorityNewReleaseWeight] = strconv.FormatInt(entry.PriorityNewReleaseWeight, 10)
		values[KeyPriorityGapWeight] = strconv.FormatInt(entry.PriorityGapWeight, 10)
		values[KeyPriorityUpgradeWeight] = strconv.FormatInt(entry.PriorityUpgradeWeight, 10)
		values[KeyPriorityNewReleaseDays] = strconv.FormatInt(entry.PriorityNewReleaseDays, 10)
	}
	if len(entry.MetadataSourceOrder) > 0 {
		encodedOrder, err := json.Marshal(entry.MetadataSourceOrder)
		if err != nil {
			return fmt.Errorf("settings.metadata_source_order: %w", err)
		}
		values[KeyMetadataSourceOrder] = string(encodedOrder)
	}
	if entry.BrainzMashUserAgent != "" {
		values[KeyBrainzMashUserAgent] = entry.BrainzMashUserAgent
	}
	if entry.MetadataCoverSources != nil {
		encoded, err := json.Marshal(*entry.MetadataCoverSources)
		if err != nil {
			return fmt.Errorf("settings.metadata_cover_sources: %w", err)
		}
		values[KeyMetadataCoverSources] = string(encoded)
	}
	if entry.MetadataTVSources != nil {
		encoded, err := json.Marshal(*entry.MetadataTVSources)
		if err != nil {
			return fmt.Errorf("settings.metadata_tv_sources: %w", err)
		}
		values[KeyMetadataTVSources] = string(encoded)
	}
	if entry.MetadataWikidata != nil {
		values[KeyMetadataWikidata] = strconv.FormatBool(*entry.MetadataWikidata)
	}
	if entry.MetadataAcoustID != nil {
		values[KeyMetadataAcoustID] = strconv.FormatBool(*entry.MetadataAcoustID)
	}
	if entry.MetadataTheAudioDB != nil {
		values[KeyMetadataTheAudioDB] = strconv.FormatBool(*entry.MetadataTheAudioDB)
	}
	if entry.RSSFreshHours != nil {
		values[KeyRSSFreshHours] = strconv.FormatInt(*entry.RSSFreshHours, 10)
	}
	if entry.AutomaticSearchesPerHour != nil {
		values[KeySearchesPerHour] = strconv.FormatInt(*entry.AutomaticSearchesPerHour, 10)
	}
	if grace := entry.AvailabilityGraceHours; grace != nil {
		values[KeyAvailabilityGraceHigh] = strconv.FormatInt(grace.High, 10)
		values[KeyAvailabilityGraceMedium] = strconv.FormatInt(grace.Medium, 10)
		values[KeyAvailabilityGraceLow] = strconv.FormatInt(grace.Low, 10)
	}
	if entry.MetadataPeopleBackfillPerRun != nil {
		values[KeyMetadataPeopleBackfillPerRun] = strconv.FormatInt(*entry.MetadataPeopleBackfillPerRun, 10)
	}
	if entry.RecycleBinMaxBytes != nil {
		values[KeyRecycleBinMaxBytes] = strconv.FormatInt(*entry.RecycleBinMaxBytes, 10)
	}
	if entry.RecycleBinMaxPercentOfRoot != nil {
		values[KeyRecycleBinMaxPercentOfRoot] = strconv.FormatInt(*entry.RecycleBinMaxPercentOfRoot, 10)
	}
	if entry.BackupRetention != "" {
		values[KeyBackupRetention] = string(entry.BackupRetention)
		values[KeyBackupKeepCount] = strconv.FormatInt(entry.BackupKeepCount, 10)
		values[KeyBackupKeepDays] = strconv.FormatInt(entry.BackupKeepDays, 10)
	}
	if entry.UsersNewLibraryPermission != "" {
		values[KeyUsersNewLibraryPermission] = string(entry.UsersNewLibraryPermission)
	}
	at := cl.Now().Unix()
	for key, value := range values {
		if err := queries.SetInstanceSetting(ctx, settingsdb.SetInstanceSettingParams{
			Key: key, ValueJson: value, UpdatedAt: at,
		}); err != nil {
			return fmt.Errorf("settings.%s: %w", key, err)
		}
	}
	return nil
}

// exportedMediaType leaves music out, so an export of a music-only instance reads as before.
func exportedMediaType(mediaType string) string {
	if mediaType == "music" {
		return ""
	}
	return mediaType
}
