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

// Document is configuration in a form a person can read, diff and share (F9).
//
// It carries policy, never identity or credentials. Accounts, password hashes
// and API keys are deliberately absent: the point of an export is that it can
// be attached to a bug report or committed to a dotfiles repository, and
// anything in it will eventually be somewhere public.
//
// Identifiers are absent too. Rows are matched on their natural keys -- a
// category by media type and name, a root folder by path -- so a document from
// one install applies to another, where the numbers would mean different things.
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

// AttachedRoot names a root by path rather than by id, so the attachment
// survives the export.
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

// DocumentVersion is the shape of the document, not the product's version. It
// changes when a field is renamed or removed, so an old export is rejected with
// an explanation rather than silently half-applied.
const DocumentVersion = 1

// Export renders the current configuration.
//
// Ordering is stable -- by natural key, not by id -- so exporting twice produces
// an identical file and a diff shows only what actually changed. An export that
// reshuffles itself is unusable in version control, which is most of the point.
func (c *Cache) Export() Document {
	snapshot := c.Snapshot()

	roots := snapshot.RootFolders()
	pathOf := make(map[int64]string, len(roots))
	document := Document{Version: DocumentVersion}
	for _, root := range roots {
		pathOf[root.ID] = root.Path
		document.Roots = append(document.Roots, RootEntry{Path: root.Path, Label: root.Label})
	}
	// ListRootFolders already orders by path, so this sort changes nothing
	// today and no test can distinguish it. It is kept deliberately: stable
	// output is a promise this document makes to version control, and resting
	// that promise on the ORDER BY of a query written for the interface means
	// it breaks the day that query is retuned, in a file nobody re-reads.
	//
	// The category sort below is not in that position -- ListCategories orders
	// by priority_rank, so it genuinely reorders, and TestExport_IsStable fails
	// without it.
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
				// A dangling attachment is not exportable as a path. Skipping it
				// keeps the document valid; the reference itself is a data
				// problem for the integrity check, not for the exporter.
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

// Validate reports every problem with a document, not the first.
//
// An import is arbitrary input -- a hand-edited file, an export from a newer
// version, something from a forum post -- so it is checked whole before anything
// is written (section 14.4). Reporting one error at a time turns a five-mistake
// file into five round trips.
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
			// An attachment naming a root the document does not define would
			// import as a category with a silently missing disc.
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

// Import applies a document, or changes nothing.
//
// Everything happens in one transaction: a partial import would leave settings
// in a state the user never chose and cannot easily undo (section 14.4). It
// merges on natural keys rather than replacing, because replacing would mean
// deleting categories that hold a library to satisfy a file.
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

				// The same rule CreateCategory follows: administrators are
				// granted manage explicitly and visibly (ADR-27), in the same
				// transaction, or the category exists with nobody able to
				// grant access to it.
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
