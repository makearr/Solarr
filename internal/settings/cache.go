package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// ErrInUse reports a configuration entity that something still references (Invariant 41).
var ErrInUse = errors.New("settings: entity is still referenced")

// ErrNotFound reports an id that is not in settings.db.
var ErrNotFound = errors.New("settings: not found")

// Cache is the in-memory authority for reads.
type Cache struct {
	store   *store.Store
	queries *settingsdb.Queries
	// current is swapped atomically. A reader takes the pointer once and holds
	// it for the whole request, so no lock is held while work is done.
	current atomic.Pointer[Snapshot]
	// rebuilding serialises build-and-publish.
	rebuilding sync.Mutex
	// backupOnChange is optional (EnableBackupOnChange): without it, a write never
	// backs up settings.db.
	backupOnChange *backupOnChange
	logger         *slog.Logger
}

// backupOnChange is config.Backup's settings_on_change policy, in the form Cache
// needs to act on it (ADR-25: settings.db is tiny and precious).
type backupOnChange struct {
	dir  string
	keep int
}

// EnableBackupOnChange makes every successful write back settings.db up and prune to
// keep, best-effort: a failed backup is logged, never returned to the caller whose
// settings change already committed. Without calling this, Cache never backs up on
// its own -- a plain NewCache never does, which is what every existing caller gets.
func (c *Cache) EnableBackupOnChange(dir string, keep int, logger *slog.Logger) {
	c.backupOnChange = &backupOnChange{dir: dir, keep: keep}
	if logger != nil {
		c.logger = logger
	}
}

// NewCache builds a cache and loads it.
func NewCache(ctx context.Context, settingsStore *store.Store) (*Cache, error) {
	cache := &Cache{
		store:   settingsStore,
		queries: settingsdb.New(settingsStore.Read()),
		logger:  slog.Default(),
	}
	if err := cache.Reload(ctx); err != nil {
		return nil, err
	}
	return cache, nil
}

// Snapshot returns the current view. It never blocks and never returns nil.
func (c *Cache) Snapshot() *Snapshot { return c.current.Load() }

// Reload rebuilds the snapshot from the database and publishes it atomically.
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
	qualityProfiles, err := c.queries.ListQualityProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list quality profiles: %w", err)
	}
	languageProfiles, err := c.queries.ListLanguageProfiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list language profiles: %w", err)
	}

	stored, err := c.queries.ListInstanceSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: list instance settings: %w", err)
	}
	policy := make(map[string]string, len(stored))
	for _, row := range stored {
		policy[row.Key] = row.ValueJson
	}

	access := make(map[int64]map[int64]Permission, len(grants))
	for _, grant := range grants {
		if access[grant.UserID] == nil {
			access[grant.UserID] = make(map[int64]Permission)
		}
		access[grant.UserID][grant.CategoryID] = Permission(grant.Permission)
	}

	convertedLanguageProfiles, err := convertLanguageProfiles(languageProfiles)
	if err != nil {
		return nil, err
	}

	return newSnapshot(
		convertUsers(users),
		convertCategories(categories),
		convertRootFolders(rootFolders),
		convertAttachments(attachments),
		access,
		convertQualityProfiles(qualityProfiles),
		convertedLanguageProfiles,
		policy,
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
			ID:                row.ID,
			MediaType:         row.MediaType,
			Name:              row.Name,
			PriorityRank:      row.PriorityRank,
			QualityProfileID:  nullInt64Ptr(row.QualityProfileID),
			LanguageProfileID: nullInt64Ptr(row.LanguageProfileID),
		})
	}
	return categories
}

// nullInt64Ptr turns a nullable database column into the pointer form the
// snapshot's typed accessors use, since "not set" and "set to zero" must stay
// distinguishable.
func nullInt64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	id := v.Int64
	return &id
}

func convertQualityProfiles(rows []settingsdb.QualityProfile) []QualityProfile {
	profiles := make([]QualityProfile, 0, len(rows))
	for _, row := range rows {
		profiles = append(profiles, QualityProfile{
			ID:          row.ID,
			Name:        row.Name,
			RulesJSON:   row.RulesJson,
			UpgradeJSON: row.UpgradeJson,
		})
	}
	return profiles
}

func convertLanguageProfiles(rows []settingsdb.LanguageProfile) ([]LanguageProfile, error) {
	profiles := make([]LanguageProfile, 0, len(rows))
	for _, row := range rows {
		audioPreferred, err := decodeStringArray(row.AudioPreferredJson)
		if err != nil {
			return nil, fmt.Errorf("settings: language profile %q audio_preferred: %w", row.Name, err)
		}
		audioRequired, err := decodeStringArray(row.AudioRequiredJson.String)
		if err != nil {
			return nil, fmt.Errorf("settings: language profile %q audio_required: %w", row.Name, err)
		}
		subtitlePreferred, err := decodeStringArray(row.SubtitlePreferredJson.String)
		if err != nil {
			return nil, fmt.Errorf("settings: language profile %q subtitle_preferred: %w", row.Name, err)
		}
		subtitleRequired, err := decodeStringArray(row.SubtitleRequiredJson.String)
		if err != nil {
			return nil, fmt.Errorf("settings: language profile %q subtitle_required: %w", row.Name, err)
		}
		profiles = append(profiles, LanguageProfile{
			ID:                  row.ID,
			Name:                row.Name,
			AudioPreferred:      audioPreferred,
			AudioRequired:       audioRequired,
			SubtitlePreferred:   subtitlePreferred,
			SubtitleRequired:    subtitleRequired,
			PreferOriginalAudio: row.PreferOriginalAudio != 0,
			RejectDubbed:        row.RejectDubbed != 0,
		})
	}
	return profiles, nil
}

// decodeStringArray reads a nullable JSON array column. An empty string (the
// column's SQL NULL) means "no list", not "empty JSON" -- json.Unmarshal is
// never asked to parse an empty string.
func decodeStringArray(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	return values, nil
}

func convertRootFolders(rows []settingsdb.RootFolder) []RootFolder {
	folders := make([]RootFolder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, convertRootFolder(row))
	}
	return folders
}

func convertRootFolder(row settingsdb.RootFolder) RootFolder {
	return RootFolder{
		ID: row.ID, Path: row.Path, Label: row.Label.String, Online: row.Online != 0,
		Writable: row.Writable != 0, WritableReason: row.WritableReason.String,
		ReadOnly: row.ReadOnly != 0, AllowRename: row.AllowRename != 0,
		TotalBytes: nullInt64Ptr(row.TotalBytes), FreeBytes: nullInt64Ptr(row.FreeBytes),
		MinFreeBytes: row.MinFreeBytes, CapacityCheckedAt: nullInt64Ptr(row.CapacityCheckedAt),
		FSID: row.FsID.String, CapabilitiesJSON: row.CapabilitiesJson.String,
	}
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
	if err := c.Reload(ctx); err != nil {
		return err
	}
	c.backupIfEnabled(ctx)
	return nil
}

// backupIfEnabled runs EnableBackupOnChange's policy. Best-effort: the settings
// change already committed and reloaded, so a backup failure is logged and does not
// unwind it.
func (c *Cache) backupIfEnabled(ctx context.Context) {
	if c.backupOnChange == nil {
		return
	}
	if _, err := c.store.Backup(ctx, c.backupOnChange.dir, store.BackupRotation); err != nil {
		c.logger.Error("settings backup on change failed", slog.Any("error", err))
		return
	}
	if _, err := c.store.PruneBackups(c.backupOnChange.dir, c.backupOnChange.keep); err != nil {
		c.logger.Error("settings backup rotation failed", slog.Any("error", err))
	}
}
