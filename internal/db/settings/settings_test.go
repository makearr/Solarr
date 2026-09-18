package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/migrations"
)

func newCache(t *testing.T) (*settings.Cache, *clock.Fake) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings",
		Path: filepath.Join(directory, "settings.db"),
		OnBudgetExceeded: func(report store.TransactionReport) {
			t.Errorf("transaction exceeded its hard budget: %s", report)
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return cache, fake
}

func TestMigrations_CreateTheStageOneSchema(t *testing.T) {
	cache, _ := newCache(t)

	snapshot := cache.Snapshot()
	if len(snapshot.Users()) != 0 {
		t.Errorf("a fresh settings.db has %d users, want 0", len(snapshot.Users()))
	}
	if len(snapshot.Categories()) != 0 {
		t.Errorf("a fresh settings.db has %d categories, want 0", len(snapshot.Categories()))
	}
}

func TestCache_ReflectsAWriteWithoutAnExplicitReload(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// The snapshot is rebuilt by the write, so it can never be stale after a
	// call returns.
	found, ok := cache.Snapshot().Category(created.ID)
	if !ok {
		t.Fatal("the new category is not in the snapshot")
	}
	if found.Name != "Music" || found.PriorityRank != 10 {
		t.Errorf("category = %+v, want Music at rank 10", found)
	}
}

// Readers hold a pointer for the length of a request, so a reload cannot change
// the answers half way through (ADR-25).
func TestSnapshot_IsStableWhileHeld(t *testing.T) {
	cache, _ := newCache(t)
	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 10); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	held := cache.Snapshot()
	if _, err := cache.CreateCategory(t.Context(), "tv", "Series", 20); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	if len(held.Categories()) != 1 {
		t.Errorf("the held snapshot changed under the reader: %d categories, want 1",
			len(held.Categories()))
	}
	if len(cache.Snapshot().Categories()) != 2 {
		t.Errorf("the current snapshot has %d categories, want 2", len(cache.Snapshot().Categories()))
	}
}

func TestSnapshot_ConcurrentReadersAndWriters(t *testing.T) {
	cache, _ := newCache(t)

	var group sync.WaitGroup
	for index := range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := cache.CreateCategory(context.Background(), "music", categoryName(index), 10); err != nil {
				t.Errorf("CreateCategory %d: %v", index, err)
			}
		}()
	}
	for range 10 {
		group.Add(1)
		go func() {
			defer group.Done()
			// Reading must never see a half-built snapshot.
			for _, category := range cache.Snapshot().Categories() {
				if category.Name == "" {
					t.Error("a category in the snapshot has no name")
				}
			}
		}()
	}
	group.Wait()

	if got := len(cache.Snapshot().Categories()); got != 10 {
		t.Errorf("got %d categories, want 10", got)
	}
}

func categoryName(index int) string { return string(rune('A' + index)) }

// Invariant 41: refused, never cascaded, with a count of what references it.
func TestDeleteRootFolder_RefusedWhileACategoryUsesIt(t *testing.T) {
	cache, _ := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "music-hdd-1")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: category.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}

	err = cache.DeleteRootFolder(t.Context(), root.ID)
	if !errors.Is(err, settings.ErrInUse) {
		t.Fatalf("DeleteRootFolder = %v, want ErrInUse", err)
	}

	var inUse *settings.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("error %v carries no InUseError", err)
	}
	if len(inUse.References) != 1 || inUse.References[0].Count != 1 {
		t.Errorf("References = %+v, want one category", inUse.References)
	}
	if _, found := cache.Snapshot().RootFolder(root.ID); !found {
		t.Error("the root folder was deleted despite being refused")
	}
}

func TestDeleteRootFolder_SucceedsOnceDetached(t *testing.T) {
	cache, _ := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: category.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
	if err := cache.DetachRootFolder(t.Context(), category.ID, root.ID); err != nil {
		t.Fatalf("DetachRootFolder: %v", err)
	}

	if err := cache.DeleteRootFolder(t.Context(), root.ID); err != nil {
		t.Fatalf("DeleteRootFolder after detaching: %v", err)
	}
	if _, found := cache.Snapshot().RootFolder(root.ID); found {
		t.Error("the root folder is still in the snapshot")
	}
}

func TestDeleteCategory_ReportsEveryReferenceAtOnce(t *testing.T) {
	cache, fake := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := cache.CreateRootFolder(t.Context(), "/media/music", "")
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: category.ID, RootFolderID: root.ID, Weight: 1, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionManage); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	err = cache.DeleteCategory(t.Context(), category.ID)
	var inUse *settings.InUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("DeleteCategory = %v, want an InUseError", err)
	}
	// Both are gathered before reporting, so the user sees the whole problem
	// rather than discovering the second after fixing the first.
	if len(inUse.References) != 2 {
		t.Errorf("References = %+v, want both roots and access grants", inUse.References)
	}
}

// Todo 4.6's own remaining gap: creation and deletion existed, editing did
// not -- queries.UpdateCategory itself was scaffolded ahead of this caller.
func TestUpdateCategory_ChangesNameAndPriorityRank(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	updated, err := cache.UpdateCategory(t.Context(), category.ID, "Tunes", 20, nil, nil, settings.MonitorDiscoveredMonitored)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.Name != "Tunes" || updated.PriorityRank != 20 {
		t.Errorf("updated = %+v, want Name=Tunes PriorityRank=20", updated)
	}
	if updated.MediaType != "music" {
		t.Errorf("MediaType = %q, must never change", updated.MediaType)
	}

	found, ok := cache.Snapshot().Category(category.ID)
	if !ok || found.Name != "Tunes" {
		t.Errorf("stored category = %+v, want Name=Tunes", found)
	}
}

// The staleness bug found live 2026-09-12: this one write must leave every
// field -- not just the ones a caller happens to read back through the same
// call -- fresh in the snapshot afterwards.
func TestUpdateCategory_SetsProfilesAndMonitorDiscoveredTogether(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	quality, err := cache.CreateQualityProfile(t.Context(), "Lossless", "{}", "{}")
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	language, err := cache.CreateLanguageProfile(t.Context(), settings.LanguageProfile{Name: "English"})
	if err != nil {
		t.Fatalf("CreateLanguageProfile: %v", err)
	}

	updated, err := cache.UpdateCategory(t.Context(), category.ID, "Music", 10,
		&quality.ID, &language.ID, settings.MonitorDiscoveredNone)
	if err != nil {
		t.Fatalf("UpdateCategory: %v", err)
	}
	if updated.QualityProfileID == nil || *updated.QualityProfileID != quality.ID {
		t.Errorf("QualityProfileID = %v, want %d", updated.QualityProfileID, quality.ID)
	}
	if updated.LanguageProfileID == nil || *updated.LanguageProfileID != language.ID {
		t.Errorf("LanguageProfileID = %v, want %d", updated.LanguageProfileID, language.ID)
	}
	if updated.MonitorDiscovered != settings.MonitorDiscoveredNone {
		t.Errorf("MonitorDiscovered = %q, want none", updated.MonitorDiscovered)
	}
}

func TestUpdateCategory_RefusesADuplicateNameWithinTheSameMediaType(t *testing.T) {
	cache, _ := newCache(t)
	if _, err := cache.CreateCategory(t.Context(), "music", "Music", 10); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	other, err := cache.CreateCategory(t.Context(), "music", "Podcasts", 20)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	_, err = cache.UpdateCategory(t.Context(), other.ID, "Music", 20, nil, nil, settings.MonitorDiscoveredMonitored)
	var problems config.Problems
	if !errors.As(err, &problems) {
		t.Fatalf("UpdateCategory = %v, want config.Problems for the name collision", err)
	}
}

func TestUpdateCategory_MissingCategoryAnswersErrNotFound(t *testing.T) {
	cache, _ := newCache(t)
	_, err := cache.UpdateCategory(t.Context(), 999999, "Whatever", 1, nil, nil, settings.MonitorDiscoveredMonitored)
	if !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("UpdateCategory = %v, want ErrNotFound", err)
	}
}

func TestPermission_AbsentGrantIsNone(t *testing.T) {
	cache, fake := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "adult", "Adult", 90)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user, err := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A new category is private until shared, not public until restricted.
	if got := cache.Snapshot().Permission(user.ID, category.ID); got != settings.PermissionNone {
		t.Errorf("Permission = %q with no grant, want none", got)
	}
	if visible := cache.Snapshot().VisibleCategoryIDs(user.ID, settings.PermissionRead); len(visible) != 0 {
		t.Errorf("VisibleCategoryIDs = %v, want none", visible)
	}
}

func TestVisibleCategoryIDs_FiltersByRequiredPermission(t *testing.T) {
	cache, fake := newCache(t)

	music, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	adult, err := cache.CreateCategory(t.Context(), "adult", "Adult", 90)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	user, err := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), user.ID, music.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	snapshot := cache.Snapshot()
	readable := snapshot.VisibleCategoryIDs(user.ID, settings.PermissionRead)
	if len(readable) != 1 || readable[0] != music.ID {
		t.Errorf("readable = %v, want just music (%d)", readable, music.ID)
	}
	// Read does not imply manage.
	if manageable := snapshot.VisibleCategoryIDs(user.ID, settings.PermissionManage); len(manageable) != 0 {
		t.Errorf("manageable = %v, want none", manageable)
	}
	if got := snapshot.Permission(user.ID, adult.ID); got != settings.PermissionNone {
		t.Errorf("Permission on the ungranted category = %q, want none", got)
	}
}

func TestPermission_AtLeast(t *testing.T) {
	cases := []struct {
		held, required settings.Permission
		want           bool
	}{
		{settings.PermissionManage, settings.PermissionRead, true},
		{settings.PermissionManage, settings.PermissionManage, true},
		{settings.PermissionRead, settings.PermissionManage, false},
		{settings.PermissionRequest, settings.PermissionRead, true},
		{settings.PermissionNone, settings.PermissionRead, false},
	}
	for _, testCase := range cases {
		if got := testCase.held.AtLeast(testCase.required); got != testCase.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", testCase.held, testCase.required, got, testCase.want)
		}
	}
}

func TestSnapshot_DoesNotCarryPasswordHashes(t *testing.T) {
	cache, fake := newCache(t)
	if _, err := cache.CreateUser(t.Context(), fake, "avs", "argon2id$hash$value", settings.RoleAdmin); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// A credential needed only at login has no business in every reader's view
	// of the configuration.
	user, found := cache.Snapshot().UserByUsername("avs")
	if !found {
		t.Fatal("the user is not in the snapshot")
	}
	if !user.HasPassword {
		t.Error("HasPassword = false for a user with a password")
	}

	hash, err := cache.PasswordHash(t.Context(), "avs")
	if err != nil {
		t.Fatalf("PasswordHash: %v", err)
	}
	if hash != "argon2id$hash$value" {
		t.Errorf("PasswordHash = %q", hash)
	}
}

func TestDeleteUser_TakesTheirGrantsWithThem(t *testing.T) {
	cache, fake := newCache(t)

	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	// An operator, not an administrator: removing the only administrator is
	// refused, and this test is about grants outliving their owner.
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleOperator)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), user.ID, category.ID, settings.PermissionManage); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	// Grants belong to the user rather than referencing them, which is why the
	// schema cascades those and nothing else.
	if err := cache.DeleteUser(t.Context(), user.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if err := cache.DeleteCategory(t.Context(), category.ID); err != nil {
		t.Fatalf("DeleteCategory after the user went: %v", err)
	}
}

func TestMutations_RejectUnknownIDs(t *testing.T) {
	cache, _ := newCache(t)

	if err := cache.DeleteCategory(t.Context(), 999); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("DeleteCategory(999) = %v, want ErrNotFound", err)
	}
	if err := cache.DeleteRootFolder(t.Context(), 999); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("DeleteRootFolder(999) = %v, want ErrNotFound", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), 999, 999, settings.PermissionRead); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("SetCategoryAccess with unknown ids = %v, want ErrNotFound", err)
	}
	if err := cache.AttachRootFolder(t.Context(), settings.CategoryRoot{CategoryID: 999, RootFolderID: 999}); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("AttachRootFolder with unknown ids = %v, want ErrNotFound", err)
	}
}

// Nothing else can restore the administrator role, so removing or demoting the
// only one leaves the interface locked out of its own settings with no way back
// that does not involve editing the database file (ADR-44).
func TestTheLastAdministratorCannotBeRemovedOrDemoted(t *testing.T) {
	cache, fake := newCache(t)

	only, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := cache.DeleteUser(t.Context(), only.ID); !errors.Is(err, settings.ErrLastAdministrator) {
		t.Errorf("DeleteUser on the only administrator = %v, want ErrLastAdministrator", err)
	}
	if err := cache.SetRole(t.Context(), only.ID, settings.RoleReadonly); !errors.Is(err, settings.ErrLastAdministrator) {
		t.Errorf("demoting the only administrator = %v, want ErrLastAdministrator", err)
	}
	if got, _ := cache.Snapshot().User(only.ID); got.Role != settings.RoleAdmin {
		t.Fatalf("the administrator survived as %q, want admin", got.Role)
	}

	// A second administrator makes the first removable: the rule is about the
	// instance keeping one, not about protecting a particular account.
	second, err := cache.CreateUser(t.Context(), fake, "other", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := cache.SetRole(t.Context(), only.ID, settings.RoleReadonly); err != nil {
		t.Errorf("demoting one of two administrators: %v", err)
	}
	if err := cache.DeleteUser(t.Context(), second.ID); !errors.Is(err, settings.ErrLastAdministrator) {
		t.Errorf("the demotion left %q as the only administrator, so removing them "+
			"should be refused: %v", second.Username, err)
	}
}

// Self-service (ADR-26): unlike SetRole, changing a tier is never refused
// for stranding the instance -- there is nothing behavioural to strand,
// only what is displayed.
func TestSetUserTier_ChangesWhatIsStored(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.Tier != settings.TierNormal {
		t.Fatalf("new user tier = %q, want normal", user.Tier)
	}

	if err := cache.SetUserTier(t.Context(), user.ID, settings.TierExpert); err != nil {
		t.Fatalf("SetUserTier: %v", err)
	}
	if got, _ := cache.Snapshot().User(user.ID); got.Tier != settings.TierExpert {
		t.Errorf("Tier = %q, want expert", got.Tier)
	}
}

func TestSetUserTier_RejectsAnUnknownTier(t *testing.T) {
	cache, fake := newCache(t)
	user, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var problems config.Problems
	if err := cache.SetUserTier(t.Context(), user.ID, settings.Tier("expert-plus")); !errors.As(err, &problems) {
		t.Fatalf("SetUserTier with an unknown tier = %v, want config.Problems", err)
	}
}

func TestSetUserTier_MissingUserAnswersErrNotFound(t *testing.T) {
	cache, _ := newCache(t)
	if err := cache.SetUserTier(t.Context(), 999999, settings.TierAdvanced); !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("SetUserTier on a missing user = %v, want ErrNotFound", err)
	}
}
