package settings

import (
	"context"
	"database/sql"
	"fmt"
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
}

// NewAccount is everything an account is created with, written as one transaction.
type NewAccount struct {
	Username     string
	PasswordHash string
	Role         Role
	Limits       UserLimits
	// Access grants per library id; an absent library is not granted.
	Access map[int64]Permission
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
	return p == PermissionNone || p == PermissionRead || p == PermissionRequest || p == PermissionManage
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
			Expectation: "none, read, request or manage"}}
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
	if !limits.NewLibraryPermission.valid() {
		problems = append(problems, config.Problem{Path: "new_library_permission",
			Value: string(limits.NewLibraryPermission), Expectation: "none, read, request or manage"})
	}
	return problems
}

// CreateAccount adds an account with its limits and library grants. An administrator manages
// every library whatever Access says, so the interface can never hide one from it.
func (c *Cache) CreateAccount(ctx context.Context, cl clock.Clock, account NewAccount) (User, error) {
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
		row, err := queries.CreateUser(ctx, settingsdb.CreateUserParams{
			Username:             account.Username,
			PasswordHash:         sql.NullString{String: account.PasswordHash, Valid: account.PasswordHash != ""},
			Role:                 string(account.Role),
			CreatedAt:            cl.Now().Unix(),
			UiTier:               string(account.Limits.TierDefault),
			UiTierDefault:        string(account.Limits.TierDefault),
			UiTierMax:            string(account.Limits.TierMax),
			NewLibraryPermission: string(account.Limits.NewLibraryPermission),
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
		return nil
	})
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
			NewLibraryPermission: string(limits.NewLibraryPermission),
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
	tier, tierDefault, tierMax, newLibrary string,
) User {
	limits := UserLimits{
		TierDefault: Tier(tierDefault), TierMax: Tier(tierMax), NewLibraryPermission: Permission(newLibrary),
	}
	effective := Tier(tier)
	if !limits.TierMax.AtLeast(effective) {
		effective = limits.TierMax
	}
	return User{
		ID: id, Username: username,
		HasPassword: hash.Valid && hash.String != "",
		Role:        Role(role), CreatedAt: time.Unix(createdAt, 0).UTC(),
		Tier: effective, Limits: limits,
	}
}
