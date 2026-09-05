package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/store/gen/settingsdb"
)

// Reference is one kind of thing holding on to an entity, and how many there are (§6).
type Reference struct {
	Kind  string
	Count int64
}

// InUseError reports a refused deletion and everything that caused it.
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

// CreateCategory adds a category and grants every administrator manage on it (ADR-27).
func (c *Cache) CreateCategory(ctx context.Context, mediaType, name string, priorityRank int64) (Category, error) {
	if problems := validateCategory(mediaType, name, priorityRank); len(problems) > 0 {
		return Category{}, problems
	}

	administrators := c.administratorIDs()

	var created settingsdb.Category
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateCategory(ctx, settingsdb.CreateCategoryParams{
			MediaType: mediaType, Name: name, PriorityRank: priorityRank,
		})
		if err != nil {
			return err
		}
		created = row
		for _, administrator := range administrators {
			if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
				UserID: administrator, CategoryID: row.ID, Permission: string(PermissionManage),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Category{}, fmt.Errorf("settings: create category %q: %w", name, err)
	}
	return Category{ID: created.ID, MediaType: created.MediaType, Name: created.Name,
		PriorityRank: created.PriorityRank, MonitorDiscovered: created.MonitorDiscovered}, nil
}

// SetCategoryMonitorDiscovered sets what a scan does with an album it finds on
// disk that nobody asked for (section 19.6, todo 4.4) -- chosen per category,
// not globally, so the live and test libraries can differ.
func (c *Cache) SetCategoryMonitorDiscovered(ctx context.Context, id int64, policy string) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if problems := validateMonitorDiscovered(policy); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryMonitorDiscovered(ctx,
			settingsdb.SetCategoryMonitorDiscoveredParams{MonitorDiscovered: policy, ID: id})
	})
}

// DeleteCategory removes a category, or refuses and says what still uses it (Invariant 41).
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
	discoveryJobs, err := c.queries.CountDiscoveryJobsUsingCategory(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		return fmt.Errorf("settings: count discovery jobs for category %d: %w", id, err)
	}

	var references []Reference
	if roots > 0 {
		references = append(references, Reference{Kind: "attached root folders", Count: roots})
	}
	if grants > 0 {
		references = append(references, Reference{Kind: "user access grants", Count: grants})
	}
	if discoveryJobs > 0 {
		references = append(references, Reference{Kind: "discovery jobs", Count: discoveryJobs})
	}
	if len(references) > 0 {
		return &InUseError{Entity: "category", ID: id, References: references}
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteCategory(ctx, id)
	})
}

// CreateQualityProfile adds a quality profile (ADR-40). Deep validation of
// rulesJSON's shape belongs to internal/score, the only package that interprets
// it; this layer checks only what settings.db itself must never hold: a name and
// syntactically valid JSON (ADR-44 -- a bad policy must be refused on write, not
// discovered later at startup).
func (c *Cache) CreateQualityProfile(ctx context.Context, name, rulesJSON, upgradeJSON string) (QualityProfile, error) {
	if problems := validateQualityProfile(name, rulesJSON, upgradeJSON); len(problems) > 0 {
		return QualityProfile{}, problems
	}

	var created settingsdb.QualityProfile
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateQualityProfile(ctx, settingsdb.CreateQualityProfileParams{
			Name: name, RulesJson: rulesJSON, UpgradeJson: upgradeJSON,
		})
		created = row
		return err
	})
	if err != nil {
		return QualityProfile{}, fmt.Errorf("settings: create quality profile %q: %w", name, err)
	}
	return QualityProfile{ID: created.ID, Name: created.Name, RulesJSON: created.RulesJson,
		UpgradeJSON: created.UpgradeJson}, nil
}

// UpdateQualityProfile replaces a quality profile's policy.
func (c *Cache) UpdateQualityProfile(ctx context.Context, id int64, name, rulesJSON, upgradeJSON string) error {
	if _, found := c.Snapshot().QualityProfile(id); !found {
		return fmt.Errorf("%w: quality profile %d", ErrNotFound, id)
	}
	if problems := validateQualityProfile(name, rulesJSON, upgradeJSON); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateQualityProfile(ctx, settingsdb.UpdateQualityProfileParams{
			Name: name, RulesJson: rulesJSON, UpgradeJson: upgradeJSON, ID: id,
		})
	})
}

// DeleteQualityProfile removes a quality profile, or refuses and says which
// categories still default to it. A desired_item in library.db may also still
// reference it (ADR-25 forbids the cross-database FK that would let this check
// see that) -- not yet possible to create, since desired_item creation for music
// is itself blocked on this profile system existing at all.
func (c *Cache) DeleteQualityProfile(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().QualityProfile(id); !found {
		return fmt.Errorf("%w: quality profile %d", ErrNotFound, id)
	}
	categories, err := c.queries.CountCategoriesUsingQualityProfile(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		return fmt.Errorf("settings: count categories for quality profile %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "quality profile", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteQualityProfile(ctx, id)
	})
}

// CreateLanguageProfile adds a language profile (section 19.8).
func (c *Cache) CreateLanguageProfile(ctx context.Context, profile LanguageProfile) (LanguageProfile, error) {
	if problems := validateLanguageProfile(profile.Name); len(problems) > 0 {
		return LanguageProfile{}, problems
	}
	audioPreferred, err := encodeStringArray(profile.AudioPreferred)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode audio_preferred: %w", err)
	}
	audioRequired, err := encodeOptionalStringArray(profile.AudioRequired)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode audio_required: %w", err)
	}
	subtitlePreferred, err := encodeOptionalStringArray(profile.SubtitlePreferred)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode subtitle_preferred: %w", err)
	}
	subtitleRequired, err := encodeOptionalStringArray(profile.SubtitleRequired)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode subtitle_required: %w", err)
	}

	var created settingsdb.LanguageProfile
	err = c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateLanguageProfile(ctx, settingsdb.CreateLanguageProfileParams{
			Name:                  profile.Name,
			AudioPreferredJson:    audioPreferred,
			AudioRequiredJson:     audioRequired,
			SubtitlePreferredJson: subtitlePreferred,
			SubtitleRequiredJson:  subtitleRequired,
			PreferOriginalAudio:   boolToInt(profile.PreferOriginalAudio),
			RejectDubbed:          boolToInt(profile.RejectDubbed),
		})
		created = row
		return err
	})
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: create language profile %q: %w", profile.Name, err)
	}
	saved, found := c.Snapshot().LanguageProfile(created.ID)
	if !found {
		return LanguageProfile{}, fmt.Errorf("settings: language profile %d vanished after create", created.ID)
	}
	return saved, nil
}

// UpdateLanguageProfile replaces a language profile's policy.
func (c *Cache) UpdateLanguageProfile(ctx context.Context, id int64, profile LanguageProfile) error {
	if _, found := c.Snapshot().LanguageProfile(id); !found {
		return fmt.Errorf("%w: language profile %d", ErrNotFound, id)
	}
	if problems := validateLanguageProfile(profile.Name); len(problems) > 0 {
		return problems
	}
	audioPreferred, err := encodeStringArray(profile.AudioPreferred)
	if err != nil {
		return fmt.Errorf("settings: encode audio_preferred: %w", err)
	}
	audioRequired, err := encodeOptionalStringArray(profile.AudioRequired)
	if err != nil {
		return fmt.Errorf("settings: encode audio_required: %w", err)
	}
	subtitlePreferred, err := encodeOptionalStringArray(profile.SubtitlePreferred)
	if err != nil {
		return fmt.Errorf("settings: encode subtitle_preferred: %w", err)
	}
	subtitleRequired, err := encodeOptionalStringArray(profile.SubtitleRequired)
	if err != nil {
		return fmt.Errorf("settings: encode subtitle_required: %w", err)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateLanguageProfile(ctx, settingsdb.UpdateLanguageProfileParams{
			Name:                  profile.Name,
			AudioPreferredJson:    audioPreferred,
			AudioRequiredJson:     audioRequired,
			SubtitlePreferredJson: subtitlePreferred,
			SubtitleRequiredJson:  subtitleRequired,
			PreferOriginalAudio:   boolToInt(profile.PreferOriginalAudio),
			RejectDubbed:          boolToInt(profile.RejectDubbed),
			ID:                    id,
		})
	})
}

// DeleteLanguageProfile removes a language profile, or refuses and says which
// categories still default to it.
func (c *Cache) DeleteLanguageProfile(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().LanguageProfile(id); !found {
		return fmt.Errorf("%w: language profile %d", ErrNotFound, id)
	}
	categories, err := c.queries.CountCategoriesUsingLanguageProfile(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		return fmt.Errorf("settings: count categories for language profile %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "language profile", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteLanguageProfile(ctx, id)
	})
}

// SetCategoryQualityProfile sets or clears (nil) a category's default quality profile.
func (c *Cache) SetCategoryQualityProfile(ctx context.Context, categoryID int64, profileID *int64) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if profileID != nil {
		if _, found := snapshot.QualityProfile(*profileID); !found {
			return fmt.Errorf("%w: quality profile %d", ErrNotFound, *profileID)
		}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryQualityProfile(ctx, settingsdb.SetCategoryQualityProfileParams{
			QualityProfileID: int64PtrToNull(profileID), ID: categoryID,
		})
	})
}

// SetCategoryLanguageProfile sets or clears (nil) a category's default language profile.
func (c *Cache) SetCategoryLanguageProfile(ctx context.Context, categoryID int64, profileID *int64) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if profileID != nil {
		if _, found := snapshot.LanguageProfile(*profileID); !found {
			return fmt.Errorf("%w: language profile %d", ErrNotFound, *profileID)
		}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryLanguageProfile(ctx, settingsdb.SetCategoryLanguageProfileParams{
			LanguageProfileID: int64PtrToNull(profileID), ID: categoryID,
		})
	})
}

func int64PtrToNull(id *int64) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

func encodeStringArray(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func encodeOptionalStringArray(values []string) (sql.NullString, error) {
	if len(values) == 0 {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

// CreateRootFolder adds a root folder.
func (c *Cache) CreateRootFolder(ctx context.Context, path, label string) (RootFolder, error) {
	if problems := validateRootFolder(path); len(problems) > 0 {
		return RootFolder{}, problems
	}

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
	return convertRootFolder(created), nil
}

// SetRootFolderPolicy writes the user's own placement policy for one root
// (section 11.7, Invariant 69) -- read_only, allow_rename and the reserve.
// Never touched by the capacity job, which only ever writes probed facts
// via SetRootFolderCapacity.
func (c *Cache) SetRootFolderPolicy(ctx context.Context, id int64, readOnly, allowRename bool, minFreeBytes int64) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}
	if problems := validateRootFolderPolicy(minFreeBytes); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderPolicy(ctx, settingsdb.SetRootFolderPolicyParams{
			ReadOnly: boolToInt(readOnly), AllowRename: boolToInt(allowRename),
			MinFreeBytes: minFreeBytes, ID: id,
		})
	})
}

// RootFolderCapacity is everything a capacity probe found about one root,
// for SetRootFolderCapacity -- grouped so the job that calls it (the only
// writer of these columns) passes one value rather than seven positional
// ones.
type RootFolderCapacity struct {
	Writable         bool
	WritableReason   string
	TotalBytes       int64
	FreeBytes        int64
	CheckedAt        int64
	FSID             string
	CapabilitiesJSON string
}

// SetRootFolderCapacity records what the capacity-polling job found --
// section 11.7: "capacity is polled, not assumed". The only writer of
// writable/total_bytes/free_bytes/fs_id; user policy lives in
// SetRootFolderPolicy instead, so the two paths can never race each other's
// columns.
func (c *Cache) SetRootFolderCapacity(ctx context.Context, id int64, capacity RootFolderCapacity) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderCapacity(ctx, settingsdb.SetRootFolderCapacityParams{
			Writable:          boolToInt(capacity.Writable),
			WritableReason:    sql.NullString{String: capacity.WritableReason, Valid: capacity.WritableReason != ""},
			TotalBytes:        sql.NullInt64{Int64: capacity.TotalBytes, Valid: true},
			FreeBytes:         sql.NullInt64{Int64: capacity.FreeBytes, Valid: true},
			CapacityCheckedAt: sql.NullInt64{Int64: capacity.CheckedAt, Valid: true},
			FsID:              sql.NullString{String: capacity.FSID, Valid: capacity.FSID != ""},
			CapabilitiesJson:  sql.NullString{String: capacity.CapabilitiesJSON, Valid: capacity.CapabilitiesJSON != ""},
			ID:                id,
		})
	})
}

// PlacementAffinity answers what an affinity key last resolved to (section
// 11.7's COMMIT step) -- placement.Resolve is pure, so this lookup and
// SetPlacementAffinity below are what a caller runs around it.
func (c *Cache) PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error) {
	rootFolderID, err := c.queries.GetPlacementAffinity(ctx, settingsdb.GetPlacementAffinityParams{
		CategoryID: categoryID, AffinityKey: affinityKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("settings: placement affinity %q: %w", affinityKey, err)
	}
	return rootFolderID, true, nil
}

// SetPlacementAffinity commits Resolve's choice for an affinity key, so the
// next item sharing it lands on the same root.
func (c *Cache) SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetPlacementAffinity(ctx, settingsdb.SetPlacementAffinityParams{
			CategoryID: categoryID, AffinityKey: affinityKey, RootFolderID: rootFolderID, UpdatedAt: updatedAt,
		})
	})
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

// CreateUser adds an account.
// SetPassword replaces an account's password hash. An empty hash leaves the
// account unable to sign in, which is what an open-access install stores.
func (c *Cache) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetUserPasswordHash(ctx, settingsdb.SetUserPasswordHashParams{
			ID:           userID,
			PasswordHash: sql.NullString{String: passwordHash, Valid: passwordHash != ""},
		})
	})
}

func (c *Cache) CreateUser(ctx context.Context, cl clock.Clock, username, passwordHash string, role Role) (User, error) {
	var categories []int64
	if role == RoleAdmin {
		for _, category := range c.Snapshot().Categories() {
			categories = append(categories, category.ID)
		}
	}

	var created settingsdb.AppUser
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateUser(ctx, settingsdb.CreateUserParams{
			Username:     username,
			PasswordHash: sql.NullString{String: passwordHash, Valid: passwordHash != ""},
			Role:         string(role),
			CreatedAt:    cl.Now().Unix(),
			UiTier:       string(TierNormal),
		})
		if err != nil {
			return err
		}
		created = row
		for _, categoryID := range categories {
			if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
				UserID: row.ID, CategoryID: categoryID, Permission: string(PermissionManage),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return User{}, fmt.Errorf("settings: create user %q: %w", username, err)
	}
	user, _ := c.Snapshot().User(created.ID)
	return user, nil
}

// DeleteUser removes an account.
func (c *Cache) DeleteUser(ctx context.Context, id int64) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if err := c.wouldStrandTheInstance(user, RoleReadonly); err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteUser(ctx, id)
	})
}

// SetRole changes what a user may do outside any one category.
func (c *Cache) SetRole(ctx context.Context, id int64, role Role) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if err := c.wouldStrandTheInstance(user, role); err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetUserRole(ctx, settingsdb.SetUserRoleParams{ID: id, Role: string(role)})
	})
}

// RevokeCategoryAccess removes a grant entirely. An absent row denies, so this
// is not the same as storing "none" -- it returns the user to the default.
func (c *Cache) RevokeCategoryAccess(ctx context.Context, userID, categoryID int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.RevokeCategoryAccess(ctx, settingsdb.RevokeCategoryAccessParams{
			UserID: userID, CategoryID: categoryID,
		})
	})
}

// ErrLastAdministrator reports a change that would leave nobody able to
// administer the instance.
var ErrLastAdministrator = errors.New("settings: that would leave no administrator")

// wouldStrandTheInstance refuses removing or demoting the only administrator.
// Nothing else can restore the role, so the interface would be locked out of its
// own settings with no way back that does not involve the database file
// (ADR-44).
func (c *Cache) wouldStrandTheInstance(user User, becoming Role) error {
	if user.Role != RoleAdmin || becoming == RoleAdmin {
		return nil
	}
	if len(c.administratorIDs()) > 1 {
		return nil
	}
	return fmt.Errorf("%w: %q is the only one", ErrLastAdministrator, user.Username)
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

// PasswordHash reads a user's stored hash.
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

// CreateExclusionPattern adds an unwanted-file pattern (ADR-53 part 1).
// Builtin is always false here -- only the shipped migration defaults are
// ever marked builtin, so a pattern the user adds is never mistaken for one.
func (c *Cache) CreateExclusionPattern(ctx context.Context, pattern ExclusionPattern) (ExclusionPattern, error) {
	if problems := validateExclusionPattern(pattern.Pattern, pattern.Action, pattern.MediaType,
		pattern.CategoryID, c.categoryExists); len(problems) > 0 {
		return ExclusionPattern{}, problems
	}

	var created settingsdb.ExclusionPattern
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateExclusionPattern(ctx, settingsdb.CreateExclusionPatternParams{
			Pattern: pattern.Pattern, Action: pattern.Action, MediaType: sql.NullString{String: pattern.MediaType, Valid: pattern.MediaType != ""},
			CategoryID: int64PtrToNull(pattern.CategoryID), Note: pattern.Note,
			Builtin: 0, Enabled: boolToInt(pattern.Enabled),
		})
		created = row
		return err
	})
	if err != nil {
		return ExclusionPattern{}, fmt.Errorf("settings: create exclusion pattern %q: %w", pattern.Pattern, err)
	}
	return convertExclusionPatterns([]settingsdb.ExclusionPattern{created})[0], nil
}

// UpdateExclusionPattern replaces a pattern's content -- Builtin never
// changes after creation, only what a builtin pattern says and whether it is
// enabled (still editable and removable, per ADR-53's own text).
func (c *Cache) UpdateExclusionPattern(ctx context.Context, id int64, pattern ExclusionPattern) error {
	if _, found := c.Snapshot().ExclusionPattern(id); !found {
		return fmt.Errorf("%w: exclusion pattern %d", ErrNotFound, id)
	}
	if problems := validateExclusionPattern(pattern.Pattern, pattern.Action, pattern.MediaType,
		pattern.CategoryID, c.categoryExists); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateExclusionPattern(ctx, settingsdb.UpdateExclusionPatternParams{
			Pattern: pattern.Pattern, Action: pattern.Action, MediaType: sql.NullString{String: pattern.MediaType, Valid: pattern.MediaType != ""},
			CategoryID: int64PtrToNull(pattern.CategoryID), Note: pattern.Note,
			Enabled: boolToInt(pattern.Enabled), ID: id,
		})
	})
}

// DeleteExclusionPattern removes a pattern outright, builtin or not -- ADR-53
// is explicit that a shipped default stays "editable and removable", never
// protected from deletion the way an in-use quality profile is.
func (c *Cache) DeleteExclusionPattern(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().ExclusionPattern(id); !found {
		return fmt.Errorf("%w: exclusion pattern %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteExclusionPattern(ctx, id)
	})
}

// categoryExists adapts Snapshot().Category for validateExclusionPattern's
// own injected lookup, so the validator stays a pure function of its
// arguments rather than reaching into Cache state itself.
func (c *Cache) categoryExists(id int64) bool {
	_, found := c.Snapshot().Category(id)
	return found
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// administratorIDs lists the accounts that get manage on every category.
func (c *Cache) administratorIDs() []int64 {
	var ids []int64
	for _, user := range c.Snapshot().Users() {
		if user.Role == RoleAdmin {
			ids = append(ids, user.ID)
		}
	}
	return ids
}
