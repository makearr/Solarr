package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// TaxonomyTag is one tag of a taxonomy's shipped tree, with the names providers give it.
type TaxonomyTag struct {
	Name     string
	Aliases  []string
	Children []TaxonomyTag
}

// taxonomySource marks a tag the shipped tree created; like a provider's, a person may rename, move or merge it.
const taxonomySource = "taxonomy"

// ErrTagMove refuses a move or merge across taxonomies, or of a tag under itself.
var ErrTagMove = errors.New("library: a tag moves only within its taxonomy, never under itself")

// taxonomySlug is a tag's unique key: a taxonomy's tags carry its key, a free label none.
func taxonomySlug(taxonomy, name string) string {
	slug := slugify(name)
	if taxonomy == "" || slug == "" {
		return slug
	}
	return taxonomy + "/" + slug
}

// SeedTaxonomy applies a taxonomy's shipped tree once per version: missing tags are created, a provider's tag of the
// same name or alias joins the shipped one, and every tag still without a parent goes under the tag its name ends with.
func (p People) SeedTaxonomy(ctx context.Context, taxonomy string, version int64, tree []TaxonomyTag) error {
	seeded, err := librarydb.New(p.store.Read()).GetTaxonomySeed(ctx, taxonomy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("library: taxonomy %s: %w", taxonomy, err)
	}
	if err == nil && seeded >= version {
		return nil
	}
	owners := map[string]int{}
	var count func(nodes []TaxonomyTag)
	count = func(nodes []TaxonomyTag) {
		for _, node := range nodes {
			for _, alias := range node.Aliases {
				owners[slugify(alias)]++
			}
			count(node.Children)
		}
	}
	count(tree)
	var seed func(queries *librarydb.Queries, nodes []TaxonomyTag, parentID int64) error
	seed = func(queries *librarydb.Queries, nodes []TaxonomyTag, parentID int64) error {
		for _, node := range nodes {
			tag, err := queries.GetTagBySlug(ctx, taxonomySlug(taxonomy, node.Name))
			switch {
			case errors.Is(err, sql.ErrNoRows):
				id, err := createTag(ctx, queries, node.Name, taxonomy, taxonomySource, parentID)
				if err != nil {
					return err
				}
				tag.ID = id
			case err != nil:
				return err
			default:
				if tag.Source != userTagSource && tag.Name != node.Name {
					if err := queries.SetTagName(ctx, librarydb.SetTagNameParams{Name: node.Name, Slug: tag.Slug, ID: tag.ID}); err != nil {
						return err
					}
				}
				if !tag.ParentID.Valid && parentID != 0 {
					if err := moveTag(ctx, queries, tag.ID, parentID); err != nil {
						return err
					}
				}
			}
			for _, alias := range node.Aliases {
				if err := queries.AddTagAlias(ctx, librarydb.AddTagAliasParams{TagID: tag.ID, Alias: slugify(alias)}); err != nil {
					return err
				}
				if owners[slugify(alias)] == 1 {
					if err := absorbAliasTag(ctx, queries, taxonomy, alias, tag.ID); err != nil {
						return err
					}
				}
			}
			if err := seed(queries, node.Children, tag.ID); err != nil {
				return err
			}
		}
		return nil
	}
	// One write per top-level genre keeps each within the bulk-write budget.
	for _, top := range tree {
		err := p.store.Write(ctx, store.PrioBulk, func(_ context.Context, tx *sql.Tx) error {
			return seed(librarydb.New(tx), []TaxonomyTag{top}, 0)
		})
		if err != nil {
			return fmt.Errorf("library: seed taxonomy %s: %w", taxonomy, err)
		}
	}
	err = p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetTaxonomySeed(ctx, librarydb.SetTaxonomySeedParams{Key: taxonomy, SeededVersion: version})
	})
	if err != nil {
		return fmt.Errorf("library: seed taxonomy %s: %w", taxonomy, err)
	}
	return p.tidyTaxonomy(ctx, taxonomy)
}

// tidyTaxonomy spells each provider's genre as the shipped tree does and puts each parentless one under the tag its
// name ends with, a page at a time.
func (p People) tidyTaxonomy(ctx context.Context, taxonomy string) error {
	tags, err := librarydb.New(p.store.Read()).ListTagsOfTaxonomy(ctx, taxonomy)
	if err != nil {
		return fmt.Errorf("library: tags of %s: %w", taxonomy, err)
	}
	for start := 0; start < len(tags); start += tagPageSize {
		page := tags[start:min(start+tagPageSize, len(tags))]
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, tag := range page {
				if tag.Source != userTagSource && tag.Source != taxonomySource && titleWords(tag.Name) != tag.Name {
					if err := queries.SetTagName(ctx, librarydb.SetTagNameParams{Name: titleWords(tag.Name), Slug: tag.Slug, ID: tag.ID}); err != nil {
						return err
					}
				}
				if tag.ParentID.Valid {
					continue
				}
				parentID, err := suffixParent(ctx, queries, taxonomy, tag.Slug)
				if err != nil {
					return err
				}
				if parentID != 0 {
					if err := moveTag(ctx, queries, tag.ID, parentID); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: place tags of %s: %w", taxonomy, err)
		}
	}
	return nil
}

// tagPageSize bounds one write over tags (CLAUDE.md: bulk work in 200-row chunks).
const tagPageSize = 200

// absorbAliasTag merges a provider's tag named like an alias ("pop music") into the shipped tag the alias belongs to.
func absorbAliasTag(ctx context.Context, queries *librarydb.Queries, taxonomy, alias string, intoID int64) error {
	other, err := queries.GetTagBySlug(ctx, taxonomySlug(taxonomy, alias))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if other.ID == intoID || other.Source == userTagSource {
		return nil
	}
	inside, err := queries.IsTagInSubtree(ctx, librarydb.IsTagInSubtreeParams{TagID: other.ID, CandidateID: intoID})
	if err != nil || inside > 0 {
		return err
	}
	return mergeTag(ctx, queries, other, intoID)
}

// suffixParent is the taxonomy's tag whose name is the longest whole-word ending of slug's, 0 when none.
func suffixParent(ctx context.Context, queries *librarydb.Queries, taxonomy, slug string) (int64, error) {
	words := strings.Split(strings.TrimPrefix(slug, taxonomy+"/"), "-")
	for start := 1; start < len(words); start++ {
		ending := strings.Join(words[start:], "-")
		parent, err := queries.GetTagBySlug(ctx, taxonomy+"/"+ending)
		if err == nil {
			return parent.ID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		aliased, err := queries.ListTagsByAlias(ctx, librarydb.ListTagsByAliasParams{Alias: ending, Taxonomy: taxonomy})
		if err != nil {
			return 0, err
		}
		if len(aliased) == 1 {
			return aliased[0], nil
		}
	}
	return 0, nil
}

// createTag adds a tag under parentID (0 for none) with its closure rows.
func createTag(ctx context.Context, queries *librarydb.Queries, name, taxonomy, source string, parentID int64) (int64, error) {
	tag, err := queries.CreateTag(ctx, librarydb.CreateTagParams{
		Name: name, Slug: taxonomySlug(taxonomy, name), Source: source, Taxonomy: taxonomy,
		ParentID: sql.NullInt64{Int64: parentID, Valid: parentID != 0},
	})
	if err != nil {
		return 0, fmt.Errorf("create tag %q: %w", name, err)
	}
	if err := queries.AddTagSelfClosure(ctx, librarydb.AddTagSelfClosureParams{AncestorID: tag.ID, DescendantID: tag.ID}); err != nil {
		return 0, fmt.Errorf("self closure: %w", err)
	}
	if parentID != 0 {
		if err := queries.AddTagClosure(ctx, librarydb.AddTagClosureParams{DescendantID: tag.ID, ParentID: parentID}); err != nil {
			return 0, fmt.Errorf("closure: %w", err)
		}
	}
	return tag.ID, nil
}

// resolveGenre is the taxonomy's tags a provider genre names: by alias, then by name, else a new tag placed under
// the tag its name ends with.
func resolveGenre(ctx context.Context, queries *librarydb.Queries, taxonomy, name, source string) ([]int64, error) {
	ids, err := queries.ListTagsByAlias(ctx, librarydb.ListTagsByAliasParams{Alias: slugify(name), Taxonomy: taxonomy})
	if err != nil || len(ids) > 0 {
		return ids, err
	}
	slug := taxonomySlug(taxonomy, name)
	existing, err := queries.GetTagBySlug(ctx, slug)
	if err == nil {
		return []int64{existing.ID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	parentID := int64(0)
	if taxonomy != "" {
		if parentID, err = suffixParent(ctx, queries, taxonomy, slug); err != nil {
			return nil, err
		}
	}
	if taxonomy != "" {
		name = titleWords(name)
	}
	id, err := createTag(ctx, queries, name, taxonomy, source, parentID)
	return []int64{id}, err
}

// titleWords capitalises each word of a provider's genre ("dance-pop" to "Dance-Pop"), as the shipped tree is spelled;
// the slug, and so every match, is unchanged.
func titleWords(name string) string {
	runes := []rune(name)
	for i, r := range runes {
		if i == 0 || runes[i-1] == ' ' || runes[i-1] == '-' {
			runes[i] = unicode.ToUpper(r)
		}
	}
	return string(runes)
}

// assignGenres makes genres the work's provider tags of the taxonomy, as source; a person's own stay (ADR-33).
func assignGenres(ctx context.Context, queries *librarydb.Queries, workID int64, taxonomy, source string, genres []string) error {
	if err := queries.ClearWorkProviderTagsOfTaxonomy(ctx, librarydb.ClearWorkProviderTagsOfTaxonomyParams{
		WorkID: workID, Taxonomy: taxonomy,
	}); err != nil {
		return fmt.Errorf("clear genres: %w", err)
	}
	for _, genre := range genres {
		ids, err := resolveGenre(ctx, queries, taxonomy, genre, source)
		if err != nil {
			return fmt.Errorf("genre %q: %w", genre, err)
		}
		for _, id := range ids {
			if err := queries.AssignTag(ctx, librarydb.AssignTagParams{TagID: id, SubjectType: "work", SubjectID: workID, Source: source}); err != nil {
				return fmt.Errorf("assign genre %q: %w", genre, err)
			}
		}
	}
	return nil
}

// ConvertClassificationGenres moves genres stored before they were tags into the taxonomy taxonomyOf answers for
// the work's media type, a page of works at a time; it answers how many works it converted.
func (p People) ConvertClassificationGenres(ctx context.Context, taxonomyOf func(mediaType string) string) (int, error) {
	converted := 0
	for {
		rows, err := librarydb.New(p.store.Read()).ListClassificationGenres(ctx, tagPageSize)
		if err != nil {
			return converted, fmt.Errorf("library: genres to convert: %w", err)
		}
		if len(rows) == 0 {
			return converted, nil
		}
		err = p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for start := 0; start < len(rows); {
				end := start
				for end < len(rows) && rows[end].WorkID == rows[start].WorkID {
					end++
				}
				var genres []string
				for _, row := range rows[start:end] {
					genres = append(genres, row.Value)
				}
				taxonomy := taxonomyOf(rows[start].MediaType)
				if taxonomy != "" {
					if err := assignGenres(ctx, queries, rows[start].WorkID, taxonomy, rows[start].Source, genres); err != nil {
						return err
					}
				}
				if err := queries.ClearClassificationGenres(ctx, rows[start].WorkID); err != nil {
					return err
				}
				converted++
				start = end
			}
			return nil
		})
		if err != nil {
			return converted, fmt.Errorf("library: convert genres: %w", err)
		}
	}
}

// TagNode is one tag of the tree as the Tags page shows it.
type TagNode struct {
	ID       int64
	Name     string
	ParentID int64
	Taxonomy string
	Source   string
	Works    int64
	Aliases  []string
}

// TagTree answers every tag of a taxonomy ("*" for all), by name, with its aliases and how many works carry it.
func (p People) TagTree(ctx context.Context, taxonomy string) ([]TagNode, error) {
	queries := librarydb.New(p.store.Read())
	rows, err := queries.ListTagTree(ctx, taxonomy)
	if err != nil {
		return nil, fmt.Errorf("library: tag tree: %w", err)
	}
	aliases, err := queries.ListTagAliases(ctx)
	if err != nil {
		return nil, fmt.Errorf("library: tag aliases: %w", err)
	}
	byTag := map[int64][]string{}
	for _, alias := range aliases {
		byTag[alias.TagID] = append(byTag[alias.TagID], alias.Alias)
	}
	nodes := make([]TagNode, len(rows))
	for i, row := range rows {
		nodes[i] = TagNode{
			ID: row.ID, Name: row.Name, ParentID: row.ParentID.Int64, Taxonomy: row.Taxonomy, Source: row.Source,
			Works: row.Works, Aliases: byTag[row.ID],
		}
	}
	return nodes, nil
}

// CreateTag adds a person's tag to a taxonomy ("" for a free label) under parentID (0 for none).
func (p People) CreateTag(ctx context.Context, name, taxonomy string, parentID int64) (int64, error) {
	name = strings.TrimSpace(name)
	if slugify(name) == "" {
		return 0, fmt.Errorf("library: create tag: %w", ErrTagName)
	}
	var id int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if _, err := queries.GetTagBySlug(ctx, taxonomySlug(taxonomy, name)); err == nil {
			return ErrTagExists
		}
		if parentID != 0 {
			parent, err := queries.GetTag(ctx, parentID)
			if err != nil {
				return err
			}
			if parent.Taxonomy != taxonomy {
				return ErrTagMove
			}
		}
		created, err := createTag(ctx, queries, name, taxonomy, userTagSource, parentID)
		id = created
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: create tag %q: %w", name, err)
	}
	return id, nil
}

// ErrTagName refuses a tag name without a letter or digit.
var ErrTagName = errors.New("library: a tag name needs a letter or digit")

// EditTag renames a tag and puts it under parentID (0 for the top); a provider's tag keeps its old name as an alias,
// so the next refresh finds it again.
func (p People) EditTag(ctx context.Context, id int64, name string, parentID int64) error {
	name = strings.TrimSpace(name)
	if slugify(name) == "" {
		return fmt.Errorf("library: edit tag %d: %w", id, ErrTagName)
	}
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		tag, err := queries.GetTag(ctx, id)
		if err != nil {
			return err
		}
		if name != tag.Name {
			slug := taxonomySlug(tag.Taxonomy, name)
			if existing, err := queries.GetTagBySlug(ctx, slug); err == nil && existing.ID != id {
				return ErrTagExists
			}
			if tag.Source != userTagSource {
				if err := queries.AddTagAlias(ctx, librarydb.AddTagAliasParams{TagID: id, Alias: slugify(tag.Name)}); err != nil {
					return err
				}
			}
			if err := queries.SetTagName(ctx, librarydb.SetTagNameParams{Name: name, Slug: slug, ID: id}); err != nil {
				return err
			}
		}
		if parentID != tag.ParentID.Int64 {
			return moveTagChecked(ctx, queries, tag, parentID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: edit tag %d: %w", id, err)
	}
	return nil
}

// moveTagChecked moves tag under parentID after refusing another taxonomy or its own subtree.
func moveTagChecked(ctx context.Context, queries *librarydb.Queries, tag librarydb.Tag, parentID int64) error {
	if parentID != 0 {
		parent, err := queries.GetTag(ctx, parentID)
		if err != nil {
			return err
		}
		inside, err := queries.IsTagInSubtree(ctx, librarydb.IsTagInSubtreeParams{TagID: tag.ID, CandidateID: parentID})
		if err != nil {
			return err
		}
		if parent.Taxonomy != tag.Taxonomy || inside > 0 {
			return ErrTagMove
		}
	}
	return moveTag(ctx, queries, tag.ID, parentID)
}

// moveTag puts a tag and its subtree under parentID (0 for the top), the closure following.
func moveTag(ctx context.Context, queries *librarydb.Queries, id, parentID int64) error {
	if err := queries.CutTagSubtree(ctx, id); err != nil {
		return fmt.Errorf("cut subtree: %w", err)
	}
	if err := queries.SetTagParentColumn(ctx, librarydb.SetTagParentColumnParams{
		ParentID: sql.NullInt64{Int64: parentID, Valid: parentID != 0}, ID: id,
	}); err != nil {
		return fmt.Errorf("set parent: %w", err)
	}
	if parentID == 0 {
		return nil
	}
	if err := queries.JoinTagSubtree(ctx, librarydb.JoinTagSubtreeParams{ParentID: parentID, TagID: id}); err != nil {
		return fmt.Errorf("join subtree: %w", err)
	}
	return nil
}

// MergeTag folds one tag into another of the same taxonomy: its works, aliases and children move over and its name
// becomes an alias, so a provider naming it later lands on the kept tag.
func (p People) MergeTag(ctx context.Context, fromID, intoID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		from, err := queries.GetTag(ctx, fromID)
		if err != nil {
			return err
		}
		into, err := queries.GetTag(ctx, intoID)
		if err != nil {
			return err
		}
		inside, err := queries.IsTagInSubtree(ctx, librarydb.IsTagInSubtreeParams{TagID: fromID, CandidateID: intoID})
		if err != nil {
			return err
		}
		if from.Taxonomy != into.Taxonomy || inside > 0 {
			return ErrTagMove
		}
		return mergeTag(ctx, queries, from, intoID)
	})
	if err != nil {
		return fmt.Errorf("library: merge tag %d into %d: %w", fromID, intoID, err)
	}
	return nil
}

// mergeTag moves from's children, works and aliases to intoID, adds its name as an alias and deletes it.
func mergeTag(ctx context.Context, queries *librarydb.Queries, from librarydb.Tag, intoID int64) error {
	children, err := queries.ListTagChildren(ctx, sql.NullInt64{Int64: from.ID, Valid: true})
	if err != nil {
		return err
	}
	for _, child := range children {
		if err := moveTag(ctx, queries, child, intoID); err != nil {
			return err
		}
	}
	if err := queries.MoveTagAssignments(ctx, librarydb.MoveTagAssignmentsParams{IntoID: intoID, FromID: from.ID}); err != nil {
		return err
	}
	if err := queries.MoveTagAliases(ctx, librarydb.MoveTagAliasesParams{IntoID: intoID, FromID: from.ID}); err != nil {
		return err
	}
	if err := queries.AddTagAlias(ctx, librarydb.AddTagAliasParams{TagID: intoID, Alias: slugify(from.Name)}); err != nil {
		return err
	}
	_, err = queries.DeleteTag(ctx, from.ID)
	return err
}

// DeleteTag removes a tag and takes it off every work; its children move up to its parent. A provider's genre comes
// back with the next refresh unless merged instead.
func (p People) DeleteTag(ctx context.Context, id int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		tag, err := queries.GetTag(ctx, id)
		if err != nil {
			return err
		}
		children, err := queries.ListTagChildren(ctx, sql.NullInt64{Int64: id, Valid: true})
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := moveTag(ctx, queries, child, tag.ParentID.Int64); err != nil {
				return err
			}
		}
		_, err = queries.DeleteTag(ctx, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("library: delete tag %d: %w", id, err)
	}
	return nil
}
