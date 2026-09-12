package apiserver_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/makearr/MediaNexus/internal/api/apiserver"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
)

func int64Ptr(v int64) *int64 { return &v }

func TestIsNearReserve(t *testing.T) {
	cases := []struct {
		name         string
		online       bool
		freeBytes    *int64
		minFreeBytes int64
		want         bool
	}{
		{"below the 10% margin", true, int64Ptr(105), 100, true},
		{"comfortably above it", true, int64Ptr(200), 100, false},
		{"offline root never warns", false, int64Ptr(50), 100, false},
		{"no reserve configured", true, int64Ptr(1), 0, false},
		{"never measured", true, nil, 100, false},
		{"exactly at the 10% margin is not yet near", true, int64Ptr(110), 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := apiserver.IsNearReserveForTest(c.online, c.freeBytes, c.minFreeBytes)
			if got != c.want {
				t.Errorf("isNearReserve(online=%v, free=%v, min=%d) = %v, want %v",
					c.online, c.freeBytes, c.minFreeBytes, got, c.want)
			}
		})
	}
}

func TestGetLibraryHealth_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(httptest.NewRequest(http.MethodGet, "/api/v1/library/health", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

// newWorksFixture never wires a LibraryHealth reader (todo 4.8's own
// dependency is optional, the same "absent means 503, not a panic" shape
// every other optional Server field already follows).
func TestGetLibraryHealth_UnavailableWithoutAReader(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/health"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

// TestCategoriesWithOfflineFiles_OnlyListsCategoriesActuallyAffected is
// todo 4.18's own remaining item ("a category with an offline root reports
// its full count with an offline note"): composed in Go from settings.db's
// real category list and library.db's own per-category counter
// (files/files_offline), the same way rootsNearReserve composes at the
// handler level rather than through the LibraryHealth interface.
func TestCategoriesWithOfflineFiles_OnlyListsCategoriesActuallyAffected(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)

	affected, err := fixture.cache.CreateCategory(t.Context(), "music", "Affected", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	unaffected, err := fixture.cache.CreateCategory(t.Context(), "tv", "Unaffected", 20)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	librarySize.byScope["category:"+strconv.FormatInt(affected.ID, 10)] = map[string]int64{"files": 12, "files_offline": 4}
	librarySize.byScope["category:"+strconv.FormatInt(unaffected.ID, 10)] = map[string]int64{"files": 7, "files_offline": 0}

	access := authz.System(fixture.cache.Snapshot())
	rows := fixture.server.CategoriesWithOfflineFilesForTest(t.Context(), access)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want exactly one affected category", rows)
	}
	if rows[0].CategoryID != affected.ID || rows[0].Name != "Affected" || rows[0].TotalFiles != 12 || rows[0].OfflineFiles != 4 {
		t.Errorf("rows[0] = %+v, want {CategoryID:%d Name:Affected TotalFiles:12 OfflineFiles:4}", rows[0], affected.ID)
	}
}

// TestCategoriesWithOfflineFiles_NeverShowsACategoryTheCallerCannotSee is
// ADR-27/Invariant 37's own guarantee, the exact shape a prior session
// caught live (todo 4.6): composing straight from settings.Snapshot instead
// of through authz.Access would leak a category's existence -- and here,
// its offline-file count -- to a user never granted it.
func TestCategoriesWithOfflineFiles_NeverShowsACategoryTheCallerCannotSee(t *testing.T) {
	librarySize := fakeLibrarySize{byScope: map[string]map[string]int64{}}
	fixture := newWorksFixtureWithLibrarySize(t, librarySize)

	affected, err := fixture.cache.CreateCategory(t.Context(), "music", "Affected", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	librarySize.byScope["category:"+strconv.FormatInt(affected.ID, 10)] = map[string]int64{"files": 12, "files_offline": 4}

	stranger := fixture.createUser(t, "stranger", settings.RoleReadonly)
	access := authz.ForUser(fixture.cache.Snapshot(), stranger)

	rows := fixture.server.CategoriesWithOfflineFilesForTest(t.Context(), access)
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want none: this caller was never granted the affected category", rows)
	}
}
