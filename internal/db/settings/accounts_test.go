package settings_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// grantsOf is every library an account can see, and at what level.
func grantsOf(cache *settings.Cache, userID int64) map[int64]settings.Permission {
	grants := map[int64]settings.Permission{}
	snapshot := cache.Snapshot()
	for _, category := range snapshot.Categories() {
		if permission := snapshot.Permission(userID, category.ID); permission != settings.PermissionNone {
			grants[category.ID] = permission
		}
	}
	return grants
}

// Access chosen at creation is exactly what the account can see; an administrator sees everything.
func TestCreateAccount_GrantsTheChosenLibrariesOnly(t *testing.T) {
	cache, fake := newCache(t)
	music, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	adult, _ := cache.CreateCategory(t.Context(), "adult", "Adult", 90)

	limits := settings.UserLimits{
		TierDefault: settings.TierAdvanced, TierMax: settings.TierAdvanced, NewLibraryPermission: settings.PermissionNone,
	}
	user, err := cache.CreateAccount(t.Context(), fake, settings.NewAccount{
		Username: "housemate", PasswordHash: "hash", Role: settings.RoleReadonly, Limits: limits,
		Access: map[int64]settings.Permission{music.ID: settings.PermissionRequest, adult.ID: settings.PermissionNone},
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if got, want := grantsOf(cache, user.ID), map[int64]settings.Permission{music.ID: settings.PermissionRequest}; !reflect.DeepEqual(got, want) {
		t.Errorf("grants = %v, want %v", got, want)
	}
	if user.Tier != settings.TierAdvanced || user.Limits != limits {
		t.Errorf("tier %q limits %+v, want advanced and %+v", user.Tier, user.Limits, limits)
	}

	admin, err := cache.CreateAccount(t.Context(), fake, settings.NewAccount{
		Username: "second", PasswordHash: "hash", Role: settings.RoleAdmin, Limits: limits,
	})
	if err != nil {
		t.Fatalf("CreateAccount admin: %v", err)
	}
	want := map[int64]settings.Permission{music.ID: settings.PermissionManage, adult.ID: settings.PermissionManage}
	if got := grantsOf(cache, admin.ID); !reflect.DeepEqual(got, want) {
		t.Errorf("administrator grants = %v, want %v", got, want)
	}
}

// A library created later is granted by each account's own new-library permission.
func TestCreateCategory_GrantsEachAccountsNewLibraryPermission(t *testing.T) {
	cache, fake := newCache(t)
	if err := cache.SetNewLibraryPermission(t.Context(), fake, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	reader, _ := cache.CreateUser(t.Context(), fake, "reader", "hash", settings.RoleReadonly)
	stranger, _ := cache.CreateAccount(t.Context(), fake, settings.NewAccount{
		Username: "stranger", PasswordHash: "hash", Role: settings.RoleReadonly,
		Limits: settings.UserLimits{
			TierDefault: settings.TierNormal, TierMax: settings.TierExpert, NewLibraryPermission: settings.PermissionNone,
		},
	})

	music, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := []settings.Permission{
		cache.Snapshot().Permission(reader.ID, music.ID), cache.Snapshot().Permission(stranger.ID, music.ID),
	}
	if want := []settings.Permission{settings.PermissionRead, settings.PermissionNone}; !reflect.DeepEqual(got, want) {
		t.Errorf("permissions = %v, want %v", got, want)
	}
}

// The maximum caps both what an account may choose and what it is shown.
func TestUserLimits_TheMaximumCapsTheLevel(t *testing.T) {
	cache, fake := newCache(t)
	user, _ := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)
	if err := cache.SetUserTier(t.Context(), user.ID, settings.TierExpert); err != nil {
		t.Fatal(err)
	}

	limits := settings.UserLimits{
		TierDefault: settings.TierNormal, TierMax: settings.TierAdvanced, NewLibraryPermission: settings.PermissionNone,
	}
	if err := cache.SetUserLimits(t.Context(), user.ID, limits); err != nil {
		t.Fatal(err)
	}
	if got, _ := cache.Snapshot().User(user.ID); got.Tier != settings.TierAdvanced {
		t.Errorf("tier = %q after lowering the maximum, want advanced", got.Tier)
	}
	var problems config.Problems
	if err := cache.SetUserTier(t.Context(), user.ID, settings.TierExpert); !errors.As(err, &problems) {
		t.Errorf("SetUserTier above the maximum = %v, want refused", err)
	}

	limits.TierDefault = settings.TierExpert
	if err := cache.SetUserLimits(t.Context(), user.ID, limits); !errors.As(err, &problems) {
		t.Errorf("default above the maximum = %v, want refused", err)
	}
}

// Promoting to administrator grants every library, as creating one does.
func TestSetRole_PromotionGrantsEveryLibrary(t *testing.T) {
	cache, fake := newCache(t)
	music, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	user, _ := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)

	if err := cache.SetRole(t.Context(), user.ID, settings.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if got := cache.Snapshot().Permission(user.ID, music.ID); got != settings.PermissionManage {
		t.Errorf("permission = %q, want manage", got)
	}
	if got, _ := cache.Snapshot().User(user.ID); got.Limits.NewLibraryPermission != settings.PermissionManage {
		t.Errorf("new-library permission = %q, want manage", got.Limits.NewLibraryPermission)
	}
}
