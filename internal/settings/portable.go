package settings

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// Document is configuration a person can read, diff and share (F9). Policy
// only, matched on natural keys. See documentation/configuration-export.md.
type Document struct {
	Version          int                    `yaml:"version"`
	Categories       []CategoryEntry        `yaml:"categories,omitempty"`
	Roots            []RootEntry            `yaml:"root_folders,omitempty"`
	QualityProfiles  []QualityProfileEntry  `yaml:"quality_profiles,omitempty"`
	LanguageProfiles []LanguageProfileEntry `yaml:"language_profiles,omitempty"`
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
// text rather than nested YAML -- internal/score, not settings, owns that shape,
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

// DocumentVersion is the document's shape, not the product's version.
const DocumentVersion = 1

// Export renders the current configuration, ordered by natural key so the file
// is stable in version control.
func (c *Cache) Export() Document {
	snapshot := c.Snapshot()

	roots := snapshot.RootFolders()
	pathOf := make(map[int64]string, len(roots))
	document := Document{Version: DocumentVersion}
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

	return document
}

// Validate reports every problem with a document, not the first (section 14.4).
func (d Document) Validate() config.Problems {
	var problems config.Problems

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

	return problems
}

// Import applies a document in one transaction, or changes nothing. Merges on
// natural keys. See documentation/configuration-export.md.
func (c *Cache) Import(ctx context.Context, document Document) error {
	if problems := document.Validate(); len(problems) > 0 {
		return problems
	}

	administrators := c.administratorIDs()
	snapshot := c.Snapshot()

	rootIDByPath := make(map[string]int64)
	for _, root := range snapshot.RootFolders() {
		rootIDByPath[root.Path] = root.ID
	}
	categoryIDByKey := make(map[string]int64)
	for _, category := range snapshot.Categories() {
		categoryIDByKey[category.MediaType+"/"+category.Name] = category.ID
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
		return nil
	})
}
