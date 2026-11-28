package settings

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// KeyUsersNewLibraryPermission is what a new non-administrator account is granted on libraries
// created after it, unless its creator chooses otherwise.
const KeyUsersNewLibraryPermission = "users.new_library_permission"

// UserLimits is what an administrator decides for an account, beyond its role and grants.
type UserLimits struct {
	// TierDefault is the interface level the account starts at.
	TierDefault Tier
	// TierMax is the most the account may raise its own level to.
	TierMax Tier
	// NewLibraryPermission is granted on every library created later; none grants nothing.
	NewLibraryPermission Permission
	// AutoApprove approves the account's requests at once; without it they wait for an administrator.
	AutoApprove bool
	// MaxAge is the oldest youngest-viewer age the account may see (13 for PG-13); nil for no limit. Unrated works stay
	// hidden unless a library of the kids kind holds them.
	MaxAge *int64
}

// UserBlockedTag is a library tag an account is kept from, by id, with its name as it was when blocked.
type UserBlockedTag struct {
	TagID int64
	Name  string
}

// NewAccount is everything an account is created with, written as one transaction.
type NewAccount struct {
	Username     string
	PasswordHash string
	Role         Role
	Limits       UserLimits
	// Access grants per library id; an absent library is not granted.
	Access map[int64]Permission
	// Blocked are the tags the account is kept from, written with the account.
	Blocked []UserBlockedTag
}

func (t Tier) rank() int {
	switch t {
	case TierAdvanced:
		return 1
	case TierExpert:
		return 2
	default:
		return 0
	}
}

func (t Tier) valid() bool { return t == TierNormal || t == TierAdvanced || t == TierExpert }

// AtLeast reports whether this tier shows everything the one asked for does.
func (t Tier) AtLeast(required Tier) bool { return t.rank() >= required.rank() }

func (p Permission) valid() bool {
	return p == PermissionNone || p == PermissionRead || p == PermissionRequest || p == PermissionAdd || p == PermissionManage
}

// NewLibraryPermission is the configured grant a new account gets on later libraries.
func (s *Snapshot) NewLibraryPermission() Permission {
	permission := Permission(s.policy[KeyUsersNewLibraryPermission])
	if !permission.valid() {
		return PermissionNone
	}
	return permission
}

// SetNewLibraryPermission writes the default for accounts created from now on.
func (c *Cache) SetNewLibraryPermission(ctx context.Context, cl clock.Clock, permission Permission) error {
	if !permission.valid() {
		return config.Problems{{Path: KeyUsersNewLibraryPermission, Value: string(permission),
			Expectation: "none, read, request, add or manage"}}
	}
	return c.setPolicy(ctx, cl, KeyUsersNewLibraryPermission, string(permission))
}

// DefaultLimits is what an account gets when its creator decides nothing: an administrator
// manages every library, anyone else gets the configured grant.
func (c *Cache) DefaultLimits(role Role) UserLimits {
	limits := UserLimits{TierDefault: TierNormal, TierMax: TierExpert, NewLibraryPermission: PermissionManage}
	if role != RoleAdmin {
		limits.NewLibraryPermission = c.Snapshot().NewLibraryPermission()
	}
	return limits
}

func validateLimits(limits UserLimits) config.Problems {
	var problems config.Problems
	if !limits.TierDefault.valid() {
		problems = append(problems, config.Problem{Path: "tier_default", Value: string(limits.TierDefault),
			Expectation: "normal, advanced or expert"})
	}
	if !limits.TierMax.valid() {
		problems = append(problems, config.Problem{Path: "tier_max", Value: string(limits.TierMax),
			Expectation: "normal, advanced or expert"})
	}
	if limits.TierDefault.valid() && limits.TierMax.valid() && !limits.TierMax.AtLeast(limits.TierDefault) {
		problems = append(problems, config.Problem{Path: "tier_default", Value: string(limits.TierDefault),
			Expectation: fmt.Sprintf("no higher than the maximum, %s", limits.TierMax)})
	}
	if limits.MaxAge != nil && (*limits.MaxAge < 0 || *limits.MaxAge > 21) {
		problems = append(problems, config.Problem{Path: "max_age", Value: strconv.FormatInt(*limits.MaxAge, 10), Expectation: "between 0 and 21, or none"})
	}
	if !limits.NewLibraryPermission.valid() {
		problems = append(problems, config.Problem{Path: "new_library_permission",
			Value: string(limits.NewLibraryPermission), Expectation: "none, read, request or manage"})
	}
	return problems
}

// CreateAccount adds an account with its limits and library grants. An administrator manages
// every library whatever Access says, so the interface can never hide one from it.
func (c *Cache) CreateAccount(ctx context.Context, cl clock.Clock, account NewAccount) (User, error) {
	return c.createAccount(ctx, cl, account, false)
}

// ErrUsernameTaken reports an account name that exists already, also when a concurrent request took it first.
var ErrUsernameTaken = errors.New("settings: an account with that name exists already")

// ErrAlreadyClaimed reports a first account asked for once an account already exists.
var ErrAlreadyClaimed = errors.New("settings: an account already exists")

// CreateFirstUser creates an account only while none exists, checked in the same transaction that writes it, so two
// concurrent claims of a fresh install cannot both succeed.
func (c *Cache) CreateFirstUser(ctx context.Context, cl clock.Clock, username, passwordHash string, role Role) (User, error) {
	return c.createAccount(ctx, cl, NewAccount{
		Username: username, PasswordHash: passwordHash, Role: role, Limits: c.DefaultLimits(role),
	}, true)
}

func (c *Cache) createAccount(ctx context.Context, cl clock.Clock, account NewAccount, first bool) (User, error) {
	problems := validateLimits(account.Limits)
	snapshot := c.Snapshot()
	grants := make(map[int64]Permission, len(account.Access))
	for categoryID, permission := range account.Access {
		if _, found := snapshot.Category(categoryID); !found {
			problems = append(problems, config.Problem{Path: "access", Value: fmt.Sprint(categoryID),
				Expectation: "an existing library"})
			continue
		}
		if !permission.valid() {
			problems = append(problems, config.Problem{Path: "access", Value: string(permission),
				Expectation: "none, read, request or manage"})
			continue
		}
		if permission != PermissionNone {
			grants[categoryID] = permission
		}
	}
	if len(problems) > 0 {
		return User{}, problems
	}
	if account.Role == RoleAdmin {
		account.Limits.NewLibraryPermission = PermissionManage
		for _, category := range snapshot.Categories() {
			grants[category.ID] = PermissionManage
		}
	}

	var created settingsdb.AppUser
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if first {
			existing, err := queries.ListUsers(ctx)
			if err != nil {
				return err
			}
			if len(existing) > 0 {
				return ErrAlreadyClaimed
			}
		}
		row, err := queries.CreateUser(ctx, settingsdb.CreateUserParams{
			Username:             account.Username,
			PasswordHash:         sql.NullString{String: account.PasswordHash, Valid: account.PasswordHash != ""},
			Role:                 string(account.Role),
			CreatedAt:            cl.Now().Unix(),
			UiTier:               string(account.Limits.TierDefault),
			UiTierDefault:        string(account.Limits.TierDefault),
			UiTierMax:            string(account.Limits.TierMax),
			NewLibraryPermission: string(account.Limits.NewLibraryPermission),
			AutoApprove:          boolToInt(account.Limits.AutoApprove),
			MaxAge:               nullInt64(account.Limits.MaxAge),
		})
		if err != nil {
			return err
		}
		created = row
		for categoryID, permission := range grants {
			if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
				UserID: row.ID, CategoryID: categoryID, Permission: string(permission),
			}); err != nil {
				return err
			}
		}
		for _, tag := range account.Blocked {
			if err := queries.InsertUserBlockedTag(ctx, settingsdb.InsertUserBlockedTagParams{UserID: row.ID, TagID: tag.TagID, TagName: tag.Name}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: app_user.username") {
		return User{}, fmt.Errorf("%w: %q", ErrUsernameTaken, account.Username)
	}
	if err != nil {
		return User{}, fmt.Errorf("settings: create user %q: %w", account.Username, err)
	}
	user, _ := c.Snapshot().User(created.ID)
	return user, nil
}

// SetUserLimits changes an account's limits. Setting the default also sets the account's current
// level, so an administrator sees the change take effect.
func (c *Cache) SetUserLimits(ctx context.Context, id int64, limits UserLimits) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if problems := validateLimits(limits); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.SetUserLimits(ctx, settingsdb.SetUserLimitsParams{
			ID: id, UiTierDefault: string(limits.TierDefault), UiTierMax: string(limits.TierMax),
			NewLibraryPermission: string(limits.NewLibraryPermission), AutoApprove: boolToInt(limits.AutoApprove), MaxAge: nullInt64(limits.MaxAge),
		}); err != nil {
			return err
		}
		if limits.TierDefault == user.Limits.TierDefault {
			return nil
		}
		return queries.SetUserTier(ctx, settingsdb.SetUserTierParams{ID: id, UiTier: string(limits.TierDefault)})
	})
}

// newLibraryGrants is what each account is granted on a library created now.
func (c *Cache) newLibraryGrants() map[int64]Permission {
	grants := make(map[int64]Permission)
	for _, user := range c.Snapshot().Users() {
		permission := user.Limits.NewLibraryPermission
		if user.Role == RoleAdmin {
			permission = PermissionManage
		}
		if permission != PermissionNone {
			grants[user.ID] = permission
		}
	}
	return grants
}

// userFromRow builds the snapshot's view of an account; its tier is clamped to its maximum, so
// lowering the maximum takes effect without touching the stored choice.
func userFromRow(id int64, username string, hash sql.NullString, role string, createdAt int64,
	tier, tierDefault, tierMax, newLibrary string, autoApprove int64, maxAge sql.NullInt64,
) User {
	limits := UserLimits{
		TierDefault: Tier(tierDefault), TierMax: Tier(tierMax), NewLibraryPermission: Permission(newLibrary), AutoApprove: autoApprove == 1,
	}
	if maxAge.Valid {
		limits.MaxAge = &maxAge.Int64
	}
	effective := Tier(tier)
	if !limits.TierMax.AtLeast(effective) {
		effective = limits.TierMax
	}
	return User{
		ID: id, Username: username,
		HasPassword: hash.Valid && hash.String != "", SessionStamp: sessionStampOf(hash.String),
		Role: Role(role), CreatedAt: time.Unix(createdAt, 0).UTC(),
		Tier: effective, Limits: limits,
	}
}

// sessionStampOf digests a password hash for session cookies: short, not reversible, and new with every password.
func sessionStampOf(passwordHash string) string {
	if passwordHash == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("solarr:session-stamp:" + passwordHash))
	return hex.EncodeToString(sum[:8])
}

// AccountPatch is the parts of an account to change together; nil leaves a part alone.
type AccountPatch struct {
	Role         *Role
	Limits       *UserLimits
	Blocked      *[]UserBlockedTag
	PasswordHash *string
}

// UpdateAccount changes the parts of AccountPatch in one transaction, after checking all of them: a refusal changes
// nothing, and the last-administrator rule sees the whole change.
func (c *Cache) UpdateAccount(ctx context.Context, id int64, patch AccountPatch) error {
	c.administrators.Lock()
	defer c.administrators.Unlock()
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if patch.Limits != nil {
		if problems := validateLimits(*patch.Limits); len(problems) > 0 {
			return problems
		}
	}
	if patch.Role != nil {
		if err := c.wouldStrandTheInstance(user, *patch.Role); err != nil {
			return err
		}
	}
	categories := c.Snapshot().Categories()
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		limits := user.Limits
		if patch.Role != nil {
			if err := queries.SetUserRole(ctx, settingsdb.SetUserRoleParams{ID: id, Role: string(*patch.Role)}); err != nil {
				return err
			}
			if *patch.Role == RoleAdmin && user.Role != RoleAdmin {
				// A promoted administrator manages every library, as one created as administrator does.
				for _, category := range categories {
					if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
						UserID: id, CategoryID: category.ID, Permission: string(PermissionManage),
					}); err != nil {
						return err
					}
				}
				limits.NewLibraryPermission = PermissionManage
				if err := queries.SetUserLimits(ctx, settingsdb.SetUserLimitsParams{
					ID: id, UiTierDefault: string(limits.TierDefault), UiTierMax: string(limits.TierMax),
					NewLibraryPermission: string(limits.NewLibraryPermission), AutoApprove: boolToInt(limits.AutoApprove), MaxAge: nullInt64(limits.MaxAge),
				}); err != nil {
					return err
				}
			}
		}
		if patch.Blocked != nil {
			if err := queries.DeleteUserBlockedTags(ctx, id); err != nil {
				return err
			}
			for _, tag := range *patch.Blocked {
				if err := queries.InsertUserBlockedTag(ctx, settingsdb.InsertUserBlockedTagParams{UserID: id, TagID: tag.TagID, TagName: tag.Name}); err != nil {
					return err
				}
			}
		}
		if patch.Limits != nil {
			if err := queries.SetUserLimits(ctx, settingsdb.SetUserLimitsParams{
				ID: id, UiTierDefault: string(patch.Limits.TierDefault), UiTierMax: string(patch.Limits.TierMax),
				NewLibraryPermission: string(patch.Limits.NewLibraryPermission), AutoApprove: boolToInt(patch.Limits.AutoApprove), MaxAge: nullInt64(patch.Limits.MaxAge),
			}); err != nil {
				return err
			}
			if patch.Limits.TierDefault != user.Limits.TierDefault {
				if err := queries.SetUserTier(ctx, settingsdb.SetUserTierParams{ID: id, UiTier: string(patch.Limits.TierDefault)}); err != nil {
					return err
				}
			}
		}
		if patch.PasswordHash != nil {
			return queries.SetUserPasswordHash(ctx, settingsdb.SetUserPasswordHashParams{
				ID: id, PasswordHash: sql.NullString{String: *patch.PasswordHash, Valid: *patch.PasswordHash != ""},
			})
		}
		return nil
	})
}
