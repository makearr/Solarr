package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// Reference is one kind of thing holding on to an entity, and how many there
// are. Structured rather than a formatted string so the UI can offer to detach
// them (coding-standards §6).
type Reference struct {
	Kind  string
	Count int64
}

// InUseError reports a refused deletion and everything that caused it. The
// counts are all gathered before reporting, so the user sees the whole problem
// rather than discovering the next one after fixing the first.
type InUseError struct {
	Entity     string
	ID         int64
	References []Reference
}

func (e *InUseError) Error() string {
	parts := make([]string, 0, len(e.References))
	for _, reference := range e.References {
		parts = append(parts, fmt.Sprintf("%d %s", reference.Count, reference.Kind))
	}
	return fmt.Sprintf("settings: %s %d is still referenced by %s", e.Entity, e.ID, strings.Join(parts, " and "))
}

func (e *InUseError) Unwrap() error { return ErrInUse }

// CreateCategory adds a category.
func (c *Cache) CreateCategory(ctx context.Context, mediaType, name string, priorityRank int64) (Category, error) {
	var created settingsdb.Category
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateCategory(ctx, settingsdb.CreateCategoryParams{
			MediaType: mediaType, Name: name, PriorityRank: priorityRank,
		})
		created = row
		return err
	})
	if err != nil {
		return Category{}, fmt.Errorf("settings: create category %q: %w", name, err)
	}
	return Category{ID: created.ID, MediaType: created.MediaType, Name: created.Name,
		PriorityRank: created.PriorityRank}, nil
}

// DeleteCategory removes a category, or refuses and says what still uses it.
//
// Access grants are counted rather than cascaded. They are meaningless without
// the category, so removing them would be defensible — but Invariant 41 admits
// no exceptions, and the interface can offer to detach and retry. A rule with
// one exception acquires a second.
func (c *Cache) DeleteCategory(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}

	roots, err := c.queries.CountRootFoldersInCategory(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count roots for category %d: %w", id, err)
	}
	grants, err := c.queries.CountUsersWithCategoryAccess(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count access for category %d: %w", id, err)
	}

	var references []Reference
	if roots > 0 {
		references = append(references, Reference{Kind: "attached root folders", Count: roots})
	}
	if grants > 0 {
		references = append(references, Reference{Kind: "user access grants", Count: grants})
	}
	if len(references) > 0 {
		return &InUseError{Entity: "category", ID: id, References: references}
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteCategory(ctx, id)
	})
}

// CreateRootFolder adds a root folder.
func (c *Cache) CreateRootFolder(ctx context.Context, path, label string) (RootFolder, error) {
	var created settingsdb.RootFolder
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateRootFolder(ctx, settingsdb.CreateRootFolderParams{
			Path:   path,
			Label:  sql.NullString{String: label, Valid: label != ""},
			Online: 1,
		})
		created = row
		return err
	})
	if err != nil {
		return RootFolder{}, fmt.Errorf("settings: create root folder %q: %w", path, err)
	}
	return RootFolder{ID: created.ID, Path: created.Path, Label: created.Label.String,
		Online: created.Online != 0}, nil
}

// DeleteRootFolder removes a root folder, or refuses and says which categories
// still point at it.
func (c *Cache) DeleteRootFolder(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}

	categories, err := c.queries.CountCategoriesUsingRootFolder(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count categories for root folder %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "root folder", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteRootFolder(ctx, id)
	})
}

// SetRootFolderOnline records whether a root is currently reachable. An offline
// root is unavailable, never free space (Invariant 20a).
func (c *Cache) SetRootFolderOnline(ctx context.Context, id int64, online bool) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderOnline(ctx, settingsdb.SetRootFolderOnlineParams{
			Online: boolToInt(online), ID: id,
		})
	})
}

// AttachRootFolder puts a root folder into a category, or updates its ordering.
func (c *Cache) AttachRootFolder(ctx context.Context, attachment CategoryRoot) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(attachment.CategoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, attachment.CategoryID)
	}
	if _, found := snapshot.RootFolder(attachment.RootFolderID); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, attachment.RootFolderID)
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.AttachRootFolderToCategory(ctx, settingsdb.AttachRootFolderToCategoryParams{
			CategoryID:   attachment.CategoryID,
			RootFolderID: attachment.RootFolderID,
			Ord:          attachment.Order,
			Weight:       attachment.Weight,
			AcceptsNew:   boolToInt(attachment.AcceptsNew),
		})
	})
}

// DetachRootFolder removes a root folder from a category.
func (c *Cache) DetachRootFolder(ctx context.Context, categoryID, rootFolderID int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DetachRootFolderFromCategory(ctx,
			settingsdb.DetachRootFolderFromCategoryParams{
				CategoryID: categoryID, RootFolderID: rootFolderID,
			})
	})
}

// CreateUser adds an account. The password hash is passed already hashed;
// hashing is the caller's concern and argon2id parameters are not settings.
func (c *Cache) CreateUser(ctx context.Context, cl clock.Clock, username, passwordHash string, role Role) (User, error) {
	var created settingsdb.AppUser
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateUser(ctx, settingsdb.CreateUserParams{
			Username:     username,
			PasswordHash: sql.NullString{String: passwordHash, Valid: passwordHash != ""},
			Role:         string(role),
			CreatedAt:    cl.Now().Unix(),
			UiTier:       string(TierNormal),
		})
		created = row
		return err
	})
	if err != nil {
		return User{}, fmt.Errorf("settings: create user %q: %w", username, err)
	}
	user, _ := c.Snapshot().User(created.ID)
	return user, nil
}

// DeleteUser removes an account. Its access grants and preferences go with it:
// they belong to the user rather than referencing them, which is why the schema
// cascades those and nothing else.
func (c *Cache) DeleteUser(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().User(id); !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteUser(ctx, id)
	})
}

// SetCategoryAccess grants or changes a user's permission on one category.
func (c *Cache) SetCategoryAccess(ctx context.Context, userID, categoryID int64, permission Permission) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.User(userID); !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, userID)
	}
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
			UserID: userID, CategoryID: categoryID, Permission: string(permission),
		})
	})
}

// PasswordHash reads a user's stored hash. It is not in the snapshot, because a
// credential that is only needed at login has no business sitting in every
// reader's view of the configuration.
func (c *Cache) PasswordHash(ctx context.Context, username string) (string, error) {
	row, err := c.queries.GetUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: user %q", ErrNotFound, username)
	}
	if err != nil {
		return "", fmt.Errorf("settings: read password hash: %w", err)
	}
	return row.PasswordHash.String, nil
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
