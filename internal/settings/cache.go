package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// ErrInUse reports a configuration entity that something still references.
// Invariant 41: the deletion is refused, never cascaded, and the error says
// what is holding it.
var ErrInUse = errors.New("settings: entity is still referenced")

// ErrNotFound reports an id that is not in settings.db.
var ErrNotFound = errors.New("settings: not found")

// Cache is the in-memory authority for reads. Writers go to the database and
// then rebuild, so the snapshot is derived from the database rather than
// maintained alongside it — the two cannot disagree.
type Cache struct {
	store   *store.Store
	queries *settingsdb.Queries
	// current is swapped atomically. A reader takes the pointer once and holds
	// it for the whole request, so no lock is held while work is done.
	current atomic.Pointer[Snapshot]
	// rebuilding serialises build-and-publish. Without it two concurrent
	// reloads race: the one that read the older state can finish last and
	// overwrite the newer snapshot, so a committed write silently disappears
	// from the cache until something else reloads. Readers never take this.
	rebuilding sync.Mutex
}

// NewCache builds a cache and loads it.
func NewCache(ctx context.Context, settingsStore *store.Store) (*Cache, error) {
	cache := &Cache{
		store:   settingsStore,
		queries: settingsdb.New(settingsStore.Read()),
	}
	if err := cache.Reload(ctx); err != nil {
		return nil, err
	}
	return cache, nil
}

// Snapshot returns the current view. It never blocks and never returns nil.
func (c *Cache) Snapshot() *Snapshot { return c.current.Load() }

// Reload rebuilds the snapshot from the database and publishes it atomically.
// A reader holding the previous snapshot keeps a consistent view until it is
// finished with it.
//
// Reading and publishing happen under one lock, so the snapshot that ends up
// published is always the one built last. Two concurrent reloads would
// otherwise be free to finish out of order, and the slower build -- which saw
// less -- would win.
func (c *Cache) Reload(ctx context.Context) error {
	c.rebuilding.Lock()
	defer c.rebuilding.Unlock()

	built, err := c.build(ctx)
	if err != nil {
		return err
	}
	c.current.Store(built)
	return nil
}

func (c *Cache) build(ctx context.Context) (*Snapshot, error) {
	users, err := c.queries.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list users: %w", err)
	}
	categories, err := c.queries.ListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list categories: %w", err)
	}
	rootFolders, err := c.queries.ListRootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list root folders: %w", err)
	}
	attachments, err := c.queries.ListCategoryRootFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list category roots: %w", err)
	}
	grants, err := c.queries.ListAllCategoryAccess(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list category access: %w", err)
	}

	access := make(map[int64]map[int64]Permission, len(grants))
	for _, grant := range grants {
		if access[grant.UserID] == nil {
			access[grant.UserID] = make(map[int64]Permission)
		}
		access[grant.UserID][grant.CategoryID] = Permission(grant.Permission)
	}

	return newSnapshot(
		convertUsers(users),
		convertCategories(categories),
		convertRootFolders(rootFolders),
		convertAttachments(attachments),
		access,
	), nil
}

func convertUsers(rows []settingsdb.AppUser) []User {
	users := make([]User, 0, len(rows))
	for _, row := range rows {
		users = append(users, User{
			ID:       row.ID,
			Username: row.Username,
			// The hash is deliberately not carried into the snapshot: it is
			// needed only at login, which reads it from the database directly.
			HasPassword: row.PasswordHash.Valid && row.PasswordHash.String != "",
			Role:        Role(row.Role),
			CreatedAt:   time.Unix(row.CreatedAt, 0).UTC(),
			Tier:        Tier(row.UiTier),
		})
	}
	return users
}

func convertCategories(rows []settingsdb.Category) []Category {
	categories := make([]Category, 0, len(rows))
	for _, row := range rows {
		categories = append(categories, Category{
			ID:           row.ID,
			MediaType:    row.MediaType,
			Name:         row.Name,
			PriorityRank: row.PriorityRank,
		})
	}
	return categories
}

func convertRootFolders(rows []settingsdb.RootFolder) []RootFolder {
	folders := make([]RootFolder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, RootFolder{
			ID:     row.ID,
			Path:   row.Path,
			Label:  row.Label.String,
			Online: row.Online != 0,
		})
	}
	return folders
}

func convertAttachments(rows []settingsdb.CategoryRootFolder) []CategoryRoot {
	attachments := make([]CategoryRoot, 0, len(rows))
	for _, row := range rows {
		attachments = append(attachments, CategoryRoot{
			CategoryID:   row.CategoryID,
			RootFolderID: row.RootFolderID,
			Order:        row.Ord,
			Weight:       row.Weight,
			AcceptsNew:   row.AcceptsNew != 0,
		})
	}
	return attachments
}

// write runs a mutation through the store's priority queue and then reloads, so
// the snapshot is never stale after a call returns.
func (c *Cache) write(ctx context.Context, mutate func(context.Context, *settingsdb.Queries) error) error {
	err := c.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return mutate(ctx, c.queries.WithTx(tx))
	})
	if err != nil {
		return err
	}
	return c.Reload(ctx)
}
