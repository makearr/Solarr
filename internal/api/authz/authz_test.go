package authz_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/platform/clock"
	"github.com/makearr/MediaNexus/migrations"
)

type fixture struct {
	cache     *settings.Cache
	clock     *clock.Fake
	music     settings.Category
	adult     settings.Category
	admin     settings.User
	housemate settings.User
}

// newFixture builds a realistic arrangement: two categories, an administrator, and a housemate
// who may read one of them (ADR-27).
func newFixture(t *testing.T) fixture {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
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

	admin, err := cache.CreateUser(t.Context(), fake, "avs", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser(admin): %v", err)
	}
	housemate, err := cache.CreateUser(t.Context(), fake, "housemate", "hash", settings.RoleReadonly)
	if err != nil {
		t.Fatalf("CreateUser(housemate): %v", err)
	}
	music, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory(music): %v", err)
	}
	adult, err := cache.CreateCategory(t.Context(), "adult", "Adult", 90)
	if err != nil {
		t.Fatalf("CreateCategory(adult): %v", err)
	}
	if err := cache.SetCategoryAccess(t.Context(), housemate.ID, music.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	return fixture{cache: cache, clock: fake, music: music, adult: adult, admin: admin, housemate: housemate}
}

func (f fixture) accessFor(user settings.User) authz.Access {
	return authz.ForUser(f.cache.Snapshot(), user)
}

// Invariant 37: a category the user cannot see returns 404, never 403. The
// existence of a private library is itself information.
func TestRequire_HiddenCategoryIsNotVisibleRatherThanForbidden(t *testing.T) {
	fixture := newFixture(t)
	access := fixture.accessFor(fixture.housemate)

	err := access.Require(fixture.adult.ID, settings.PermissionRead)
	if !errors.Is(err, authz.ErrNotVisible) {
		t.Fatalf("Require on a hidden category = %v, want ErrNotVisible", err)
	}
	if errors.Is(err, authz.ErrInsufficientPermission) {
		t.Error("a hidden category reported as a permission problem, which admits it exists")
	}
}

// A category the caller can already see is a genuine 403: refusing plainly
// tells them nothing they did not know.
func TestRequire_VisibleButInsufficientIsForbidden(t *testing.T) {
	fixture := newFixture(t)
	access := fixture.accessFor(fixture.housemate)

	err := access.Require(fixture.music.ID, settings.PermissionManage)
	if !errors.Is(err, authz.ErrInsufficientPermission) {
		t.Fatalf("Require = %v, want ErrInsufficientPermission", err)
	}
	if errors.Is(err, authz.ErrNotVisible) {
		t.Error("a visible category reported as missing, which hides an ordinary permission error")
	}
}

func TestRequire_AllowsWhatIsGranted(t *testing.T) {
	fixture := newFixture(t)
	access := fixture.accessFor(fixture.housemate)

	if err := access.Require(fixture.music.ID, settings.PermissionRead); err != nil {
		t.Errorf("Require(read) on a granted category = %v, want nil", err)
	}
}

func TestRequire_UnknownCategoryLooksExactlyLikeAHiddenOne(t *testing.T) {
	// Otherwise probing ids tells an unprivileged user which ones exist.
	fixture := newFixture(t)
	access := fixture.accessFor(fixture.housemate)

	hidden := access.Require(fixture.adult.ID, settings.PermissionRead)
	absent := access.Require(999999, settings.PermissionRead)

	if !errors.Is(hidden, authz.ErrNotVisible) || !errors.Is(absent, authz.ErrNotVisible) {
		t.Fatalf("hidden = %v, absent = %v; both want ErrNotVisible", hidden, absent)
	}
}

func TestCategories_ReturnsOnlyWhatTheCallerMaySee(t *testing.T) {
	fixture := newFixture(t)

	visible := fixture.accessFor(fixture.housemate).Categories(settings.PermissionRead)
	if len(visible) != 1 || visible[0].ID != fixture.music.ID {
		t.Errorf("housemate sees %+v, want only Music", visible)
	}

	// Read does not imply manage.
	manageable := fixture.accessFor(fixture.housemate).Categories(settings.PermissionManage)
	if len(manageable) != 0 {
		t.Errorf("housemate may manage %+v, want none", manageable)
	}
}

// ADR-27: an administrator has manage on all categories, granted explicitly and visibly, so an
// audit shows who can see what — not by a role check hidden in the authorisation path.
func TestAdministrator_HasExplicitGrantsRatherThanABypass(t *testing.T) {
	fixture := newFixture(t)
	snapshot := fixture.cache.Snapshot()

	for _, category := range []settings.Category{fixture.music, fixture.adult} {
		if got := snapshot.Permission(fixture.admin.ID, category.ID); got != settings.PermissionManage {
			t.Errorf("admin permission on %q = %q, want manage", category.Name, got)
		}
	}

	visible := fixture.accessFor(fixture.admin).Categories(settings.PermissionManage)
	if len(visible) != 2 {
		t.Errorf("admin sees %d categories, want 2", len(visible))
	}
}

func TestAdministrator_GetsGrantsOnCategoriesCreatedAfterwards(t *testing.T) {
	fixture := newFixture(t)

	later, err := fixture.cache.CreateCategory(t.Context(), "tv", "Series", 20)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	// A category that existed for even a moment with no administrator able to
	// manage it would be one nobody could grant access to.
	if got := fixture.cache.Snapshot().Permission(fixture.admin.ID, later.ID); got != settings.PermissionManage {
		t.Errorf("admin permission on a later category = %q, want manage", got)
	}
}

func TestAdministrator_CreatedAfterACategoryStillGetsIt(t *testing.T) {
	fixture := newFixture(t)

	second, err := fixture.cache.CreateUser(t.Context(), fixture.clock, "second-admin", "hash", settings.RoleAdmin)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	for _, category := range []settings.Category{fixture.music, fixture.adult} {
		if got := fixture.cache.Snapshot().Permission(second.ID, category.ID); got != settings.PermissionManage {
			t.Errorf("new admin permission on %q = %q, want manage", category.Name, got)
		}
	}
}

func TestSystem_SeesEverything(t *testing.T) {
	fixture := newFixture(t)
	access := authz.System(fixture.cache.Snapshot())

	if !access.IsSystem() {
		t.Error("IsSystem() = false")
	}
	if err := access.Require(fixture.adult.ID, settings.PermissionManage); err != nil {
		t.Errorf("system Require = %v, want nil; a job acts on the whole library", err)
	}
	if got := len(access.Categories(settings.PermissionManage)); got != 2 {
		t.Errorf("system sees %d categories, want 2", got)
	}
}

// A route that skipped its middleware must fail loudly (Invariant 36).
func TestFromContext_MissingAccessIsAnError(t *testing.T) {
	if _, err := authz.FromContext(context.Background()); !errors.Is(err, authz.ErrNoAccess) {
		t.Errorf("FromContext on a bare context = %v, want ErrNoAccess", err)
	}
}

func TestFromContext_RoundTrips(t *testing.T) {
	fixture := newFixture(t)
	original := fixture.accessFor(fixture.housemate)

	recovered, err := authz.FromContext(authz.WithAccess(context.Background(), original))
	if err != nil {
		t.Fatalf("FromContext: %v", err)
	}
	if recovered.UserID() != fixture.housemate.ID {
		t.Errorf("UserID() = %d, want %d", recovered.UserID(), fixture.housemate.ID)
	}
	if !recovered.CanSee(fixture.music.ID) || recovered.CanSee(fixture.adult.ID) {
		t.Error("the recovered access does not carry the original permissions")
	}
}

// The snapshot an Access was built from is the one it keeps answering from, so
// a settings change mid-request cannot alter the answers half way through.
func TestAccess_HoldsItsSnapshotForTheWholeRequest(t *testing.T) {
	fixture := newFixture(t)
	access := fixture.accessFor(fixture.housemate)

	if err := fixture.cache.SetCategoryAccess(t.Context(),
		fixture.housemate.ID, fixture.adult.ID, settings.PermissionManage); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}

	if access.CanSee(fixture.adult.ID) {
		t.Error("an in-flight request saw a grant made after it started")
	}
	if !fixture.accessFor(fixture.housemate).CanSee(fixture.adult.ID) {
		t.Error("a new request does not see the grant")
	}
}

func TestAccess_ZeroValueSeesNothing(t *testing.T) {
	// A zero Access is what a bug produces; it must be closed, not open.
	var empty authz.Access

	if empty.CanSee(1) {
		t.Error("the zero Access can see a category")
	}
	if got := empty.Permission(1); got != settings.PermissionNone {
		t.Errorf("Permission = %q, want none", got)
	}
	if got := empty.Categories(settings.PermissionRead); len(got) != 0 {
		t.Errorf("Categories = %+v, want none", got)
	}
	if err := empty.Require(1, settings.PermissionRead); !errors.Is(err, authz.ErrNotVisible) {
		t.Errorf("Require = %v, want ErrNotVisible", err)
	}
}
