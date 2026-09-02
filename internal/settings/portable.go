package settings

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// Document is configuration a person can read, diff and share (F9). Policy
// only, matched on natural keys. See documentation/configuration-export.md.
type Document struct {
	Version    int             `yaml:"version"`
	Categories []CategoryEntry `yaml:"categories,omitempty"`
	Roots      []RootEntry     `yaml:"root_folders,omitempty"`
}

// CategoryEntry is one category and the roots attached to it.
type CategoryEntry struct {
	MediaType    string         `yaml:"media_type"`
	Name         string         `yaml:"name"`
	PriorityRank int64          `yaml:"priority_rank"`
	Roots        []AttachedRoot `yaml:"roots,omitempty"`
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

	attachments := snapshot.CategoryRoots()
	for _, category := range snapshot.Categories() {
		entry := CategoryEntry{
			MediaType:    category.MediaType,
			Name:         category.Name,
			PriorityRank: category.PriorityRank,
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
		switch {
		case strings.TrimSpace(root.Path) == "":
			problems = append(problems, config.Problem{Path: where + ".path", Expectation: "a path"})
		case !filepath.IsAbs(root.Path):
			problems = append(problems, config.Problem{
				Path: where + ".path", Value: root.Path, Expectation: "an absolute path",
			})
		case knownPaths[root.Path]:
			problems = append(problems, config.Problem{
				Path: where + ".path", Value: root.Path, Expectation: "a path listed once",
			})
		default:
			knownPaths[root.Path] = true
		}
	}

	seenCategories := make(map[string]bool, len(d.Categories))
	for index, category := range d.Categories {
		where := fmt.Sprintf("categories[%d]", index)
		if strings.TrimSpace(category.MediaType) == "" {
			problems = append(problems, config.Problem{Path: where + ".media_type", Expectation: "a media type"})
		}
		if strings.TrimSpace(category.Name) == "" {
			problems = append(problems, config.Problem{Path: where + ".name", Expectation: "a name"})
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

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
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
		}
		return nil
	})
}
