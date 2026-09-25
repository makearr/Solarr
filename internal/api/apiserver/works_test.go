package apiserver_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/migrations"
)

// testImagesDir is the fixture's fixed cover art cache directory, inside the
// in-memory filesystem every worksFixture gets.
const testImagesDir = "/images"

// fakeWorksReader records the filter it was called with and answers canned rows,
// so a test can assert what category visibility computed without a real library.db.
type fakeWorksReader struct {
	rows       []apiserver.WorkRow
	lastFilter apiserver.WorksFilter
	lastCursor apiserver.WorksCursor
	calls      int
}

func (f *fakeWorksReader) ListWorks(
	_ context.Context, filter apiserver.WorksFilter, cursor apiserver.WorksCursor, pageSize int,
) ([]apiserver.WorkRow, error) {
	f.calls++
	f.lastFilter = filter
	f.lastCursor = cursor
	if len(f.rows) > pageSize {
		return f.rows[:pageSize], nil
	}
	return f.rows, nil
}

// worksFixture wires a real settings.db (categories, root folders, access
// grants) to a Server backed by a fakeWorksReader, so category visibility runs
// through the real repository layer while the library answer stays a fake.
type worksFixture struct {
	server               *apiserver.Server
	cache                *settings.Cache
	works                *fakeWorksReader
	workDetail           *fakeWorkDetailReader
	worksMonitor         *fakeWorksMonitor
	search               *fakeSearchReader
	unidentified         *fakeUnidentifiedReader
	unidentifiedResolver *fakeUnidentifiedResolver
	artwork              *fakeArtworkReader
	people               *fakePeopleReader
	subscriber           *fakePeopleSubscriber
	subscriptionPreview  *fakeSubscriptionPreviewer
	blockedContent       *fakeBlockedContentLister
	packs                *fakePackLister
	removeAndBlocker     *fakeRemoveAndBlocker
	importSessions       *fakeImportSessionLister
	importInspector      *fakeImportInspector
	fingerprintRetrier   *fakeFingerprintRetrier
	drainStarter         *fakeDrainStarter
	moveJobs             *fakeMoveJobLister
	renameStarter        *fakeRenameStarter
	namingSchemes        *fakeNamingSchemes
	calendar             *fakeCalendar
	history              *fakeHistory
	libraryTracks        *fakeLibraryTracksReader
	scheduler            *fakeScheduler
	cacheManager         *fakeCacheManager
	discover             *fakeDiscover
	arrSync              *fakeArrSync
	indexerTester        *fakeConnectionTester
	downloaderTester     *fakeConnectionTester
	candidateSearcher    *fakeCandidateSearcher
	fsys                 *fsx.Mem
	clock                *clock.Fake
}

// fakeLibraryArtistsReader answers canned rows for the Artists navigation
// level, so a test can assert ListLibraryLevel's dispatch without a real
// library.People.
type fakeLibraryArtistsReader struct {
	rows       []apiserver.ArtistRow
	lastFilter apiserver.ArtistsFilter
	lastCursor apiserver.ArtistsCursor
}

func (f *fakeLibraryArtistsReader) ListLibraryArtists(
	_ context.Context, filter apiserver.ArtistsFilter, cursor apiserver.ArtistsCursor, _ int,
) ([]apiserver.ArtistRow, error) {
	f.lastFilter = filter
	f.lastCursor = cursor
	return f.rows, nil
}

// fakeLibraryTracksReader answers canned rows for the Tracks navigation
// level, so a test can assert ListLibraryLevel's dispatch without a real
// library.TrackParts.
type fakeLibraryTracksReader struct {
	rows       []apiserver.TrackRow
	nextCursor apiserver.TracksCursor
	hasMore    bool
	lastFilter apiserver.TracksFilter
	lastCursor apiserver.TracksCursor
}

func (f *fakeLibraryTracksReader) ListLibraryTracks(
	_ context.Context, filter apiserver.TracksFilter, cursor apiserver.TracksCursor, _ int,
) ([]apiserver.TrackRow, apiserver.TracksCursor, bool, error) {
	f.lastFilter = filter
	f.lastCursor = cursor
	return f.rows, f.nextCursor, f.hasMore, nil
}

func newWorksFixture(t *testing.T) worksFixture {
	t.Helper()
	return newWorksFixtureWithLibrarySize(t, nil)
}

// newWorksFixtureWithLibrarySize is newWorksFixture plus an injectable
// LibrarySize, for tests asserting on the counters ListCategories reads
// (categories_test.go) without every other worksFixture caller needing one.
func newWorksFixtureWithLibrarySize(t *testing.T, librarySize apiserver.LibrarySize) worksFixture {
	t.Helper()
	return newWorksFixtureWithLibrarySizeAndNavigation(t, librarySize, nil)
}

// newWorksFixtureWithLibrarySizeAndNavigation is newWorksFixtureWithLibrarySize
// plus an injectable NavigationProvider, for libraries_test.go's assertions on
// ADR-54's per-level counts without every other caller needing one.
func newWorksFixtureWithLibrarySizeAndNavigation(
	t *testing.T, librarySize apiserver.LibrarySize, navigation apiserver.NavigationProvider,
) worksFixture {
	t.Helper()
	return newWorksFixtureWithLibrarySizeNavigationAndArtists(t, librarySize, navigation, nil, nil)
}

// newWorksFixtureWithRegistry is newWorksFixture plus an injectable
// settings.Registry, for network_test.go/importsettings_test.go/
// authpolicy_test.go's proof that a write path refuses a key the registry
// does not know (ADR-26, todo 4.14) -- the mutation this guards against is
// a Set* handler forgetting its own Registry.LookupAll check.
func newWorksFixtureWithRegistry(t *testing.T, registry *settings.Registry) worksFixture {
	t.Helper()
	return newWorksFixtureWithLibrarySizeNavigationAndArtists(t, nil, nil, nil, registry)
}

// newWorksFixtureWithLibrarySizeNavigationAndArtists is
// newWorksFixtureWithLibrarySizeAndNavigation plus an injectable
// LibraryArtistsReader, for libraries_test.go's assertion that the Artists
// level dispatches when one is actually wired (nil elsewhere, so the "no
// reader yet" 503 case stays real). registry nil means
// settings.NewDefaultRegistry(), the shape every other parameter here uses.
func newWorksFixtureWithLibrarySizeNavigationAndArtists(
	t *testing.T, librarySize apiserver.LibrarySize, navigation apiserver.NavigationProvider,
	libraryArtists apiserver.LibraryArtistsReader, registry *settings.Registry,
) worksFixture {
	return newWorksFixtureWithLibrarySizeNavigationArtistsAndConfigPath(
		t, librarySize, navigation, libraryArtists, registry, "")
}

// newWorksFixtureWithConfigPath is newWorksFixture plus an injectable
// config.yml path (todo: ADR-58's revised precedence, 2026-09-12), for
// network_test.go's own proof that UpdateNetworkSettings patches the real
// file. Empty everywhere else (the default) means "config.yml write-back
// unavailable," matching the ConfigPath field's own optional shape.
func newWorksFixtureWithConfigPath(t *testing.T, configPath string) worksFixture {
	t.Helper()
	return newWorksFixtureWithLibrarySizeNavigationArtistsAndConfigPath(t, nil, nil, nil, nil, configPath)
}

func newWorksFixtureWithLibrarySizeNavigationArtistsAndConfigPath(
	t *testing.T, librarySize apiserver.LibrarySize, navigation apiserver.NavigationProvider,
	libraryArtists apiserver.LibraryArtistsReader, registry *settings.Registry, configPath string,
) worksFixture {
	t.Helper()
	if registry == nil {
		registry = settings.NewDefaultRegistry()
	}
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "settings", Path: filepath.Join(directory, "settings.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Settings()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	cache, err := settings.NewCache(t.Context(), opened)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	works := &fakeWorksReader{}
	workDetail := &fakeWorkDetailReader{}
	worksMonitor := &fakeWorksMonitor{}
	search := &fakeSearchReader{}
	unidentified := &fakeUnidentifiedReader{}
	unidentifiedResolver := &fakeUnidentifiedResolver{}
	artwork := &fakeArtworkReader{}
	people := &fakePeopleReader{}
	subscriber := &fakePeopleSubscriber{}
	subscriptionPreview := &fakeSubscriptionPreviewer{}
	blockedContent := &fakeBlockedContentLister{}
	packs := &fakePackLister{}
	removeAndBlocker := &fakeRemoveAndBlocker{}
	importSessions := &fakeImportSessionLister{}
	importInspector := &fakeImportInspector{}
	fingerprintRetrier := &fakeFingerprintRetrier{}
	drainStarter := &fakeDrainStarter{}
	moveJobs := &fakeMoveJobLister{}
	renameStarter := &fakeRenameStarter{}
	namingSchemes := &fakeNamingSchemes{}
	calendar := &fakeCalendar{}
	history := &fakeHistory{}
	libraryTracks := &fakeLibraryTracksReader{}
	scheduler := &fakeScheduler{states: map[string]jobs.ScheduleRun{}}
	cacheManager := &fakeCacheManager{}
	discover := &fakeDiscover{}
	arrSync := &fakeArrSync{}
	indexerTester := &fakeConnectionTester{}
	downloaderTester := &fakeConnectionTester{}
	candidateSearcher := &fakeCandidateSearcher{}
	fsys := fsx.NewMem(fake)
	server, err := apiserver.New(apiserver.Config{
		Bind: "127.0.0.1", Port: 0, BaseURL: "/",
		Checker:               fakeChecker{},
		Works:                 works,
		WorkDetail:            workDetail,
		WorksMonitor:          worksMonitor,
		Search:                search,
		Unidentified:          unidentified,
		UnidentifiedResolver:  unidentifiedResolver,
		Artwork:               artwork,
		People:                people,
		PeopleSubscriber:      subscriber,
		SubscriptionPreviewer: subscriptionPreview,
		BlockedContent:        blockedContent,
		Packs:                 packs,
		RemoveAndBlocker:      removeAndBlocker,
		ImportSessions:        importSessions,
		ImportInspector:       importInspector,
		FingerprintRetrier:    fingerprintRetrier,
		DrainStarter:          drainStarter,
		MoveJobs:              moveJobs,
		RenameStarter:         renameStarter,
		NamingSchemes:         namingSchemes,
		Calendar:              calendar,
		History:               history,
		FS:                    fsys,
		ConfigPath:            configPath,
		DownloadRoots:         apiserver.FilesystemRoots{FS: fsys},
		Scheduler:             scheduler,
		CacheManager:          cacheManager,
		Discover:              discover,
		ArrSync:               arrSync,
		IndexerTester:         indexerTester,
		DownloaderTester:      downloaderTester,
		CandidateSearcher:     candidateSearcher,
		ImagesDir:             testImagesDir,
		LibrarySize:           librarySize,
		Navigation:            navigation,
		LibraryTracks:         libraryTracks,
		LibraryArtists:        libraryArtists,
		Clock:                 fake,
		Logger:                slog.New(slog.DiscardHandler),
		Auth: apiserver.AuthConfig{
			Mode:     apiserver.AuthLocal,
			Sessions: auth.NewSessions([]byte("0123456789abcdef0123456789abcdef"), fake, time.Hour),
			Settings: cache,
			Registry: registry,
			Clock:    fake,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return worksFixture{
		server: server, cache: cache, works: works, workDetail: workDetail, worksMonitor: worksMonitor,
		search: search, unidentified: unidentified, unidentifiedResolver: unidentifiedResolver,
		artwork: artwork, people: people, subscriber: subscriber, subscriptionPreview: subscriptionPreview,
		blockedContent: blockedContent, packs: packs, removeAndBlocker: removeAndBlocker,
		importSessions: importSessions, importInspector: importInspector,
		fingerprintRetrier: fingerprintRetrier,
		drainStarter:       drainStarter, moveJobs: moveJobs, renameStarter: renameStarter, namingSchemes: namingSchemes, calendar: calendar,
		history:       history,
		libraryTracks: libraryTracks, scheduler: scheduler, cacheManager: cacheManager, discover: discover,
		arrSync: arrSync, indexerTester: indexerTester, downloaderTester: downloaderTester,
		candidateSearcher: candidateSearcher, fsys: fsys, clock: fake,
	}
}

func (f worksFixture) do(request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	f.server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func (f worksFixture) createUser(t *testing.T, username string, role settings.Role) settings.User {
	t.Helper()
	hashed, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := f.cache.CreateUser(t.Context(), f.clock, username, hashed, role)
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return user
}

func (f worksFixture) signIn(t *testing.T, username string) []*http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"`+username+`","password":"`+testPassword+`"}`))
	request.Header.Set("Content-Type", "application/json")

	recorder := f.do(request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("login: %d (%s)", recorder.Code, recorder.Body)
	}
	return recorder.Result().Cookies()
}

func (f worksFixture) createCategoryWithRoot(t *testing.T, name string, rootPath string) (settings.Category, settings.RootFolder) {
	t.Helper()
	category, err := f.cache.CreateCategory(t.Context(), "music", name, 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	root, err := f.cache.CreateRootFolder(t.Context(), rootPath, name)
	if err != nil {
		t.Fatalf("CreateRootFolder: %v", err)
	}
	if err := f.cache.AttachRootFolder(t.Context(), settings.CategoryRoot{
		CategoryID: category.ID, RootFolderID: root.ID, AcceptsNew: true,
	}); err != nil {
		t.Fatalf("AttachRootFolder: %v", err)
	}
	return category, root
}

func authedGet(t *testing.T, cookies []*http.Cookie, target string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return request
}

// An admin is a system identity (authz.Access.system), which VisibleCategoryIDs
// treats as "every category" -- so an admin still only sees works under a root
// folder some category actually claims, never truly unfiltered.
func TestListWorks_AdminSeesRootFoldersOfEveryCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	_, rootA := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootB := fixture.createCategoryWithRoot(t, "Soundtracks", "/media/soundtracks")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.works.lastFilter.RootFolderIDs
	slices.Sort(got)
	want := []int64{rootA.ID, rootB.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("RootFolderIDs = %v, want %v", got, want)
	}
}

// The core guarantee: a readonly user with access to only one category must
// never have the other category's root folder reach the library query.
func TestListWorks_ExcludesRootFoldersOfCategoriesNotGranted(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.works.lastFilter.RootFolderIDs
	if slices.Contains(got, rootHidden.ID) {
		t.Errorf("RootFolderIDs = %v, must not include the ungranted category's root %d", got, rootHidden.ID)
	}
	if !slices.Contains(got, rootVisible.ID) {
		t.Errorf("RootFolderIDs = %v, must include the granted category's root %d", got, rootVisible.ID)
	}
}

// A user granted nothing must produce a non-nil empty filter -- nil means
// unfiltered (library.WorkFilter's own distinction), which here would mean
// showing every category's works to someone who can see none of them.
func TestListWorks_NoGrantsProducesNonNilEmptyFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "nobody", settings.RoleReadonly)
	cookies := fixture.signIn(t, "nobody")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.works.lastFilter.RootFolderIDs
	if got == nil {
		t.Fatal("RootFolderIDs is nil, want a non-nil empty slice -- nil is unfiltered")
	}
	if len(got) != 0 {
		t.Errorf("RootFolderIDs = %v, want empty: this user has no category grants", got)
	}
}

func TestListWorks_CategoryIDNarrowsToOneCategory(t *testing.T) {
	fixture := newWorksFixture(t)
	first, rootFirst := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	second, _ := fixture.createCategoryWithRoot(t, "Soundtracks", "/media/soundtracks")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?category_id="+itoa(first.ID)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.works.lastFilter.RootFolderIDs
	if !slices.Equal(got, []int64{rootFirst.ID}) {
		t.Errorf("RootFolderIDs = %v, want just %d's root", got, first.ID)
	}
	_ = second
}

// Invariant 37: naming a category the caller cannot read answers 404, never 403
// (which would confirm the category exists at all) and never 200 with an
// unfiltered list (which would ignore the parameter silently).
func TestListWorks_CategoryIDForAnUngrantedCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, _ = fixture.createCategoryWithRoot(t, "Music", "/media/music")
	hidden, _ := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, hidden.ID, settings.PermissionNone); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?category_id="+itoa(hidden.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a category this caller cannot read", recorder.Code)
	}
}

func TestListWorks_ResponseShapeAndCursor(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	year := int64(1997)
	fixture.works.rows = []apiserver.WorkRow{
		{ID: 1, MediaType: "music", Title: "KI Pecezinu", SortTitle: "ki pecezinu", Year: &year,
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
		{ID: 2, MediaType: "music", Title: "Zor A", SortTitle: "zor a",
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body struct {
		Items []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Year  *int   `json:"year"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Title != "KI Pecezinu" {
		t.Fatalf("items = %+v, want exactly the first row (limit=1)", body.Items)
	}
	if body.Items[0].Year == nil || *body.Items[0].Year != 1997 {
		t.Errorf("Year = %v, want 1997", body.Items[0].Year)
	}
	if body.NextCursor == "" {
		t.Error("NextCursor is empty, want a cursor: there is a second row beyond the limit")
	}
}

func TestListWorks_ArtistFieldPassesThrough(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	artist := "Katupoken"
	fixture.works.rows = []apiserver.WorkRow{
		{ID: 1, MediaType: "music", Title: "KI Pecezinu", SortTitle: "ki pecezinu", Artist: &artist,
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body struct {
		Items []struct {
			Artist *string `json:"artist"`
		} `json:"items"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Artist == nil || *body.Items[0].Artist != "Katupoken" {
		t.Fatalf("items = %+v, want an artist of Katupoken", body.Items)
	}
}

func TestListWorks_SortAndDirReachTheFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?sort=year&dir=desc"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.works.lastFilter.Sort != apiserver.WorkSortYear {
		t.Errorf("Sort = %q, want year", fixture.works.lastFilter.Sort)
	}
	if fixture.works.lastFilter.Direction != apiserver.SortDescending {
		t.Errorf("Direction = %q, want desc", fixture.works.lastFilter.Direction)
	}
}

func TestListWorks_FreshReachesTheFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?fresh=true"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if !fixture.works.lastFilter.Fresh {
		t.Errorf("Fresh = false, want true")
	}
}

// The Wanted screen listed complete albums too: the hand-written route never read wanted.
func TestListWorks_WantedReachesTheFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?wanted=true"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if !fixture.works.lastFilter.Wanted {
		t.Errorf("Wanted = false, want true")
	}
}

func TestListWorks_FreshDefaultsToFalse(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.works.lastFilter.Fresh {
		t.Errorf("Fresh = true, want false when the query omits it")
	}
}

func TestListWorks_UnknownSortAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?sort=runtime"))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: runtime is not a valid sort column", recorder.Code)
	}
}

// A year-sorted page's next_cursor must round-trip through the year cursor
// codec, not the title one -- worksCursorFor's whole reason to exist.
func TestListWorks_YearSortedCursorRoundTrips(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	first := int64(1990)
	second := int64(2010)
	fixture.works.rows = []apiserver.WorkRow{
		{ID: 1, MediaType: "music", Title: "Older", SortTitle: "older", Year: &first,
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
		{ID: 2, MediaType: "music", Title: "Newer", SortTitle: "newer", Year: &second,
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?sort=year&limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.NextCursor == "" {
		t.Fatal("NextCursor is empty, want a cursor")
	}

	recorder = fixture.do(authedGet(t, cookies, "/api/v1/works?sort=year&cursor="+body.NextCursor))
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status %d (%s)", recorder.Code, recorder.Body)
	}
	if fixture.works.lastCursor.SortYear != 1990 || fixture.works.lastCursor.ID != 1 {
		t.Errorf("decoded cursor = %+v, want SortYear 1990, ID 1", fixture.works.lastCursor)
	}
}

// The artist order carries two text values in one cursor; a title or artist
// containing the characters a naive separator would use must survive intact.
func TestListWorks_ArtistSortedCursorRoundTrips(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	artist := "XT/QK + Friends: Live"
	fixture.works.rows = []apiserver.WorkRow{
		{ID: 7, MediaType: "music", Title: "Kata in Mifed", SortTitle: "kata in mifed: remaster | 2020",
			Artist: &artist, ArtistSortName: "XT/QK + Friends: Live",
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
		{ID: 8, MediaType: "music", Title: "Bopacem to Nedu", SortTitle: "bopacem to nedu",
			AddedAt: fixture.clock.Now(), UpdatedAt: fixture.clock.Now()},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/works?sort=artist&limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}
	var body struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.NextCursor == "" {
		t.Fatal("NextCursor is empty, want a cursor")
	}

	recorder = fixture.do(authedGet(t, cookies, "/api/v1/works?sort=artist&cursor="+body.NextCursor))
	if recorder.Code != http.StatusOK {
		t.Fatalf("second page status %d (%s)", recorder.Code, recorder.Body)
	}
	want := apiserver.WorksCursor{SortArtist: "XT/QK + Friends: Live", SortTitle: "kata in mifed: remaster | 2020", ID: 7}
	if fixture.works.lastCursor != want {
		t.Errorf("decoded cursor = %+v, want %+v", fixture.works.lastCursor, want)
	}
	if fixture.works.lastFilter.Sort != apiserver.WorkSortArtist {
		t.Errorf("filter sort = %q, want artist", fixture.works.lastFilter.Sort)
	}
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
