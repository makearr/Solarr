package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strconv"

	"github.com/makearr/MediaNexus/internal/db/store/gen/settingsdb"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// Document is configuration a person can read, diff and share (F9). Policy
// only, matched on natural keys. See documentation/configuration-export.md.
type Document struct {
	Version           int                     `yaml:"version"`
	Settings          *ScalarSettingsEntry    `yaml:"settings,omitempty"`
	Categories        []CategoryEntry         `yaml:"categories,omitempty"`
	Roots             []RootEntry             `yaml:"root_folders,omitempty"`
	QualityProfiles   []QualityProfileEntry   `yaml:"quality_profiles,omitempty"`
	LanguageProfiles  []LanguageProfileEntry  `yaml:"language_profiles,omitempty"`
	ExclusionPatterns []ExclusionPatternEntry `yaml:"exclusion_patterns,omitempty"`
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
	NetworkPort                int      `yaml:"network_port,omitempty"`
	NetworkProxyURL            string   `yaml:"network_proxy_url,omitempty"`
	NetworkProxyBypass         []string `yaml:"network_proxy_bypass,omitempty"`
	NetworkAllowCIDRs          []string `yaml:"network_allow_cidrs,omitempty"`
	CacheMaxBytes              int64    `yaml:"cache_max_bytes,omitempty"`
	CacheMaxAgeDays            int64    `yaml:"cache_max_age_days,omitempty"`
	CacheImagesSharePercent    int64    `yaml:"cache_images_share_percent,omitempty"`
	CacheMetadataSharePercent  int64    `yaml:"cache_metadata_share_percent,omitempty"`
	PriorityUserRequestWeight  int64    `yaml:"priority_user_request_weight,omitempty"`
	PriorityNewReleaseWeight   int64    `yaml:"priority_new_release_weight,omitempty"`
	PriorityGapWeight          int64    `yaml:"priority_gap_weight,omitempty"`
	PriorityUpgradeWeight      int64    `yaml:"priority_upgrade_weight,omitempty"`
	PriorityNewReleaseDays     int64    `yaml:"priority_new_release_days,omitempty"`
	BrainzMashUserAgent        string   `yaml:"brainzmash_user_agent,omitempty"`
}

// CategoryEntry is one category and the roots attached to it.
type CategoryEntry struct {
	MediaType       string         `yaml:"media_type"`
	Name            string         `yaml:"name"`
	PriorityRank    int64          `yaml:"priority_rank"`
	Roots           []AttachedRoot `yaml:"roots,omitempty"`
	QualityProfile  string         `yaml:"quality_profile,omitempty"`
	LanguageProfile string         `yaml:"language_profile,omitempty"`
	// MonitorDiscovered is section 19.6's policy: what a scan does with an
	// album it finds that nobody asked for. Empty on import means the
	// column's own default (monitored) applies.
	MonitorDiscovered string `yaml:"monitor_discovered,omitempty"`
}

// QualityProfileEntry is one ADR-40 profile. Rules and Upgrade stay opaque JSON
// text rather than nested YAML -- internal/identify/score, not settings, owns that shape,
// and settings must not need updating every time it grows a field.
type QualityProfileEntry struct {
	Name    string `yaml:"name"`
	Rules   string `yaml:"rules"`
	Upgrade string `yaml:"upgrade"`
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

// DocumentVersion is the document's shape, not the product's version.
const DocumentVersion = 1

// Export renders the current configuration, ordered by natural key so the file
// is stable in version control.
func (c *Cache) Export() Document {
	snapshot := c.Snapshot()

	network := snapshot.NetworkSettings()
	cacheBudget := snapshot.CacheBudget()
	priority := snapshot.PrioritySettings()
	settingsEntry := ScalarSettingsEntry{
		AuthRequired:               snapshot.AuthRequired(),
		ImportAutoCommitConfidence: snapshot.ImportAutoCommitConfidence(),
		ImportTransferLadder:       snapshot.ImportTransferLadder(),
		NetworkPort:                network.Port,
		NetworkProxyURL:            network.ProxyURL,
		NetworkProxyBypass:         network.ProxyBypass,
		NetworkAllowCIDRs:          network.AllowCIDRs,
		CacheMaxBytes:              cacheBudget.MaxBytes,
		CacheMaxAgeDays:            cacheBudget.MaxAgeDays,
		CacheImagesSharePercent:    cacheBudget.ImagesSharePercent,
		CacheMetadataSharePercent:  cacheBudget.MetadataSharePercent,
		PriorityUserRequestWeight:  priority.UserRequestWeight,
		PriorityNewReleaseWeight:   priority.NewReleaseWeight,
		PriorityGapWeight:          priority.GapWeight,
		PriorityUpgradeWeight:      priority.UpgradeWeight,
		PriorityNewReleaseDays:     priority.NewReleaseDays,
		BrainzMashUserAgent:        snapshot.MetadataSettings().BrainzMashUserAgent,
	}

	roots := snapshot.RootFolders()
	pathOf := make(map[int64]string, len(roots))
	document := Document{Version: DocumentVersion, Settings: &settingsEntry}
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
		})
	}
	sort.Slice(document.QualityProfiles, func(i, j int) bool {
		return document.QualityProfiles[i].Name < document.QualityProfiles[j].Name
	})

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
		if category.QualityProfileID != nil {
			entry.QualityProfile = qualityNameByID[*category.QualityProfileID]
		}
		if category.LanguageProfileID != nil {
			entry.LanguageProfile = languageNameByID[*category.LanguageProfileID]
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

	if d.Settings != nil {
		for _, problem := range validateImportAutoCommitConfidence(d.Settings.ImportAutoCommitConfidence) {
			problems = append(problems, prefixed("settings", problem))
		}
		// Absent means "say nothing about it", unlike the API setter where an
		// empty ladder is a user clearing every method and is refused.
		if len(d.Settings.ImportTransferLadder) > 0 {
			for _, problem := range validateImportTransferLadder(d.Settings.ImportTransferLadder) {
				problems = append(problems, prefixed("settings", problem))
			}
		}
		network := NetworkSettings{
			Port: d.Settings.NetworkPort, ProxyURL: d.Settings.NetworkProxyURL,
			ProxyBypass: d.Settings.NetworkProxyBypass, AllowCIDRs: d.Settings.NetworkAllowCIDRs,
		}
		for _, problem := range validateNetworkSettings(network, callerAddress) {
			problems = append(problems, prefixed("settings", problem))
		}
		cacheBudget := CacheBudget{
			MaxBytes: d.Settings.CacheMaxBytes, MaxAgeDays: d.Settings.CacheMaxAgeDays,
			ImagesSharePercent: d.Settings.CacheImagesSharePercent, MetadataSharePercent: d.Settings.CacheMetadataSharePercent,
		}
		for _, problem := range validateCacheBudget(cacheBudget) {
			problems = append(problems, prefixed("settings", problem))
		}
		priority := PrioritySettings{
			UserRequestWeight: d.Settings.PriorityUserRequestWeight, NewReleaseWeight: d.Settings.PriorityNewReleaseWeight,
			GapWeight: d.Settings.PriorityGapWeight, UpgradeWeight: d.Settings.PriorityUpgradeWeight,
			NewReleaseDays: d.Settings.PriorityNewReleaseDays,
		}
		for _, problem := range validatePrioritySettings(priority) {
			problems = append(problems, prefixed("settings", problem))
		}
		// Absent means the document predates the setting, so the stored agent is kept.
		if d.Settings.BrainzMashUserAgent != "" {
			for _, problem := range validateMetadataSettings(MetadataSettings{BrainzMashUserAgent: d.Settings.BrainzMashUserAgent}) {
				problems = append(problems, prefixed("settings", problem))
			}
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
				"a version this build understands (%d or lower); upgrade MediaNexus to read it",
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

		for rootIndex, attached := range category.Roots {
			// Or the category imports with a silently missing disc.
			if !knownPaths[attached.Path] {
				problems = append(problems, config.Problem{
					Path:        fmt.Sprintf("%s.roots[%d].path", where, rootIndex),
					Value:       attached.Path,
					Expectation: "a path also listed under root_folders",
				})
			}
		}
		if category.QualityProfile != "" && !knownQualityProfiles[category.QualityProfile] {
			problems = append(problems, config.Problem{
				Path: where + ".quality_profile", Value: category.QualityProfile,
				Expectation: "a name also listed under quality_profiles",
			})
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
	if problems := document.Validate(callerAddress); len(problems) > 0 {
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

		for _, profile := range document.QualityProfiles {
			if _, exists := qualityIDByName[profile.Name]; exists {
				continue
			}
			created, err := queries.CreateQualityProfile(ctx, settingsdb.CreateQualityProfileParams{
				Name: profile.Name, RulesJson: profile.Rules, UpgradeJson: profile.Upgrade,
			})
			if err != nil {
				return fmt.Errorf("quality profile %q: %w", profile.Name, err)
			}
			qualityIDByName[profile.Name] = created.ID
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
				Path:   root.Path,
				Label:  sql.NullString{String: root.Label, Valid: root.Label != ""},
				Online: 1,
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

			if category.QualityProfile != "" {
				if err := queries.SetCategoryQualityProfile(ctx, settingsdb.SetCategoryQualityProfileParams{
					QualityProfileID: sql.NullInt64{Int64: qualityIDByName[category.QualityProfile], Valid: true},
					ID:               id,
				}); err != nil {
					return fmt.Errorf("category %q quality profile: %w", key, err)
				}
			}
			if category.MonitorDiscovered != "" {
				if err := queries.SetCategoryMonitorDiscovered(ctx, settingsdb.SetCategoryMonitorDiscoveredParams{
					MonitorDiscovered: category.MonitorDiscovered, ID: id,
				}); err != nil {
					return fmt.Errorf("category %q monitor_discovered: %w", key, err)
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

	values := map[string]string{
		KeyAuthRequired:               strconv.FormatBool(entry.AuthRequired),
		KeyImportTransferLadder:       string(transferLadder),
		KeyImportAutoCommitConfidence: strconv.FormatFloat(entry.ImportAutoCommitConfidence, 'f', -1, 64),
		KeyNetworkPort:                strconv.Itoa(entry.NetworkPort),
		KeyNetworkProxyURL:            entry.NetworkProxyURL,
		KeyNetworkProxyBypass:         string(proxyBypass),
		KeyNetworkAllowCIDRs:          string(allowCIDRs),
		KeyCacheMaxBytes:              strconv.FormatInt(entry.CacheMaxBytes, 10),
		KeyCacheMaxAgeDays:            strconv.FormatInt(entry.CacheMaxAgeDays, 10),
		KeyCacheImagesShare:           strconv.FormatInt(entry.CacheImagesSharePercent, 10),
		KeyCacheMetadataShare:         strconv.FormatInt(entry.CacheMetadataSharePercent, 10),
		KeyPriorityUserRequestWeight:  strconv.FormatInt(entry.PriorityUserRequestWeight, 10),
		KeyPriorityNewReleaseWeight:   strconv.FormatInt(entry.PriorityNewReleaseWeight, 10),
		KeyPriorityGapWeight:          strconv.FormatInt(entry.PriorityGapWeight, 10),
		KeyPriorityUpgradeWeight:      strconv.FormatInt(entry.PriorityUpgradeWeight, 10),
		KeyPriorityNewReleaseDays:     strconv.FormatInt(entry.PriorityNewReleaseDays, 10),
	}
	if entry.BrainzMashUserAgent != "" {
		values[KeyBrainzMashUserAgent] = entry.BrainzMashUserAgent
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
