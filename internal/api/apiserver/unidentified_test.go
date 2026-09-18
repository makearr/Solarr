package apiserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// fakeUnidentifiedReader mirrors fakeWorksReader: it records the root folder ids
// it was called with and answers canned rows, so a test can assert what category
// visibility computed without a real library.db.
type fakeUnidentifiedReader struct {
	rows              []apiserver.UnidentifiedRow
	lastRootFolderIDs []int64
	calls             int
}

func (f *fakeUnidentifiedReader) ListUnidentified(
	_ context.Context, rootFolderIDs []int64, _ paginate.Cursor, pageSize int,
) ([]apiserver.UnidentifiedRow, error) {
	f.calls++
	f.lastRootFolderIDs = rootFolderIDs
	if len(f.rows) > pageSize {
		return f.rows[:pageSize], nil
	}
	return f.rows, nil
}

// fakeUnidentifiedResolver answers a canned entry from GetUnidentified and
// records what ResolveUnidentified was called with, without a real
// library.db or musicbrainz call.
type fakeUnidentifiedResolver struct {
	entry      apiserver.UnidentifiedRow
	getErr     error
	resolveErr error
	workID     int64

	lastMediaObjectID int64
	lastProvider      string
	lastExternalID    string
	resolveCalls      int

	attempts     []apiserver.IdentificationAttempt
	attemptCalls int
}

func (f *fakeUnidentifiedResolver) IdentificationAttempts(context.Context, int64) ([]apiserver.IdentificationAttempt, error) {
	f.attemptCalls++
	return f.attempts, nil
}

func (f *fakeUnidentifiedResolver) GetUnidentified(
	_ context.Context, mediaObjectID int64,
) (apiserver.UnidentifiedRow, error) {
	if f.getErr != nil {
		return apiserver.UnidentifiedRow{}, f.getErr
	}
	f.entry.MediaObjectID = mediaObjectID
	return f.entry, nil
}

func (f *fakeUnidentifiedResolver) ResolveUnidentified(
	_ context.Context, mediaObjectID int64, provider, externalID string,
) (int64, error) {
	f.resolveCalls++
	f.lastMediaObjectID = mediaObjectID
	f.lastProvider = provider
	f.lastExternalID = externalID
	if f.resolveErr != nil {
		return 0, f.resolveErr
	}
	return f.workID, nil
}

// ResolveUnidentified confirms a candidate for a queued file the caller may
// see, and forwards exactly what the request named.
func TestResolveUnidentified_ConfirmsACandidateAndReturnsTheWorkID(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.unidentifiedResolver.entry = apiserver.UnidentifiedRow{RootFolderID: root.ID}
	fixture.unidentifiedResolver.workID = 42
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/library/unidentified/1/resolve",
		`{"provider":"musicbrainz:release-group","external_id":"rg-1"}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}

	var body struct {
		WorkID int64 `json:"work_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.WorkID != 42 {
		t.Errorf("work_id = %d, want 42", body.WorkID)
	}
	if fixture.unidentifiedResolver.lastMediaObjectID != 1 ||
		fixture.unidentifiedResolver.lastProvider != "musicbrainz:release-group" ||
		fixture.unidentifiedResolver.lastExternalID != "rg-1" {
		t.Errorf("resolver called with %+v, want the request body forwarded",
			fixture.unidentifiedResolver)
	}
}

// The core ADR-27 guarantee: a queued file under a root folder the caller
// cannot read must answer 404, indistinguishable from missing, and must
// never reach ResolveUnidentified at all.
func TestResolveUnidentified_UnreadableRootFolderAnswers404AndNeverResolves(t *testing.T) {
	fixture := newWorksFixture(t)
	_, hidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.unidentifiedResolver.entry = apiserver.UnidentifiedRow{RootFolderID: hidden.ID}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/library/unidentified/1/resolve",
		`{"provider":"musicbrainz:release-group","external_id":"rg-1"}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if fixture.unidentifiedResolver.resolveCalls != 0 {
		t.Error("ResolveUnidentified must not be reached for an unreadable root folder")
	}
}

// A missing queued entry answers 404, not 500.
func TestResolveUnidentified_MissingEntryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.unidentifiedResolver.getErr = sql.ErrNoRows
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/library/unidentified/999/resolve",
		`{"provider":"musicbrainz:release-group","external_id":"rg-1"}`))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
}

// An unsupported provider is a 400, not a 500 -- the caller sent a request
// this action cannot fulfil, not something that broke.
func TestResolveUnidentified_UnsupportedProviderAnswers400(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.unidentifiedResolver.entry = apiserver.UnidentifiedRow{RootFolderID: root.ID}
	fixture.unidentifiedResolver.resolveErr = apiserver.ErrUnsupportedProvider
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/library/unidentified/1/resolve",
		`{"provider":"tvdb","external_id":"x"}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body = %s", recorder.Code, recorder.Body)
	}
}

// Unauthenticated requests answer 401.
func TestResolveUnidentified_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)

	recorder := fixture.do(authedPost(t, nil, "/api/v1/library/unidentified/1/resolve",
		`{"provider":"musicbrainz:release-group","external_id":"rg-1"}`))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

// The core guarantee, same as TestListWorks_ExcludesRootFoldersOfCategoriesNotGranted:
// a readonly user with access to only one category must never have the other
// category's root folder reach the library query.
func TestListUnidentified_ExcludesRootFoldersOfCategoriesNotGranted(t *testing.T) {
	fixture := newWorksFixture(t)
	visible, rootVisible := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	_, rootHidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.unidentified.lastRootFolderIDs
	if slices.Contains(got, rootHidden.ID) {
		t.Errorf("root folder ids = %v, must not include the ungranted category's root %d", got, rootHidden.ID)
	}
	if !slices.Contains(got, rootVisible.ID) {
		t.Errorf("root folder ids = %v, must include the granted category's root %d", got, rootVisible.ID)
	}
}

// A user granted nothing must produce a non-nil empty filter -- nil means
// unfiltered, which here would show every category's queue to someone who can
// see none of them.
func TestListUnidentified_NoGrantsProducesNonNilEmptyFilter(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "nobody", settings.RoleReadonly)
	cookies := fixture.signIn(t, "nobody")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	got := fixture.unidentified.lastRootFolderIDs
	if got == nil {
		t.Fatal("root folder ids is nil, want a non-nil empty slice -- nil is unfiltered")
	}
	if len(got) != 0 {
		t.Errorf("root folder ids = %v, want empty: this user has no category grants", got)
	}
}

// Invariant 37, same guard as /works: naming a category the caller cannot read
// answers 404, never 403 and never 200 with an unfiltered list.
func TestListUnidentified_CategoryIDForAnUngrantedCategoryAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, _ = fixture.createCategoryWithRoot(t, "Music", "/media/music")
	hidden, _ := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, hidden.ID, settings.PermissionNone); err != nil {
		t.Fatalf("SetCategoryAccess: %v", err)
	}
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified?category_id="+itoa(hidden.ID)))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a category this caller cannot read", recorder.Code)
	}
}

func TestListUnidentified_ResponseShapeAndCursor(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createCategoryWithRoot(t, "Music", "/media/music")
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	fixture.unidentified.rows = []apiserver.UnidentifiedRow{
		{
			MediaObjectID: 1, RootFolderID: 1, RelPath: "Unsorted/one.mp3",
			Candidates:  []apiserver.UnidentifiedCandidate{{Strategy: "embedded-tag", Confidence: 0.4}},
			FirstSeenAt: fixture.clock.Now(), LastSeenAt: fixture.clock.Now(),
		},
		{
			MediaObjectID: 2, RootFolderID: 1, RelPath: "Unsorted/two.mp3",
			FirstSeenAt: fixture.clock.Now(), LastSeenAt: fixture.clock.Now(),
		},
	}

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified?limit=1"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", recorder.Code, recorder.Body)
	}

	var body struct {
		Items []struct {
			MediaObjectID int64  `json:"media_object_id"`
			RelPath       string `json:"rel_path"`
			Candidates    []struct {
				Strategy   string  `json:"strategy"`
				Confidence float64 `json:"confidence"`
			} `json:"candidates"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].RelPath != "Unsorted/one.mp3" {
		t.Fatalf("items = %+v, want exactly the first row (limit=1)", body.Items)
	}
	if len(body.Items[0].Candidates) != 1 || body.Items[0].Candidates[0].Strategy != "embedded-tag" {
		t.Errorf("candidates = %+v, want the one queued candidate", body.Items[0].Candidates)
	}
	if body.NextCursor == "" {
		t.Error("NextCursor is empty, want a cursor: there is a second row beyond the limit")
	}
}

func TestListIdentificationAttempts_AnswersEveryPassWithItsEvidence(t *testing.T) {
	fixture := newWorksFixture(t)
	_, root := fixture.createCategoryWithRoot(t, "Music", "/media/music")
	seen := time.Unix(1_600_000_000, 0).UTC()
	fixture.unidentifiedResolver.entry = apiserver.UnidentifiedRow{
		RootFolderID: root.ID, RelPath: "P!po/Kebilegev Bibihi/01.m4a", FirstSeenAt: seen, LastSeenAt: seen,
	}
	fixture.unidentifiedResolver.attempts = []apiserver.IdentificationAttempt{{
		At: time.Unix(1_700_000_000, 0).UTC(), Outcome: "queued",
		Passes: []apiserver.IdentificationPass{
			{Pass: "embedded_tags", Outcome: "none"},
			{Pass: "album_match", Outcome: "ambiguous", Candidates: []apiserver.IdentificationCandidate{{
				Strategy: "parse", Provider: "musicbrainz:release-group", ExternalID: "rg-1", Confidence: 0.25,
				Evidence: []apiserver.IdentificationEvidence{{Kind: "title", Detail: "Kebilegev Bibihi"}},
			}}},
		},
	}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified/1/attempts"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	want := `{"file":{"candidates":[],"first_seen_at":"2020-09-13T12:26:40Z","last_seen_at":"2020-09-13T12:26:40Z",` +
		fmt.Sprintf(`"media_object_id":1,"rel_path":"P!po/Kebilegev Bibihi/01.m4a","root_folder_id":%d},`, root.ID) +
		`"items":[{"attempted_at":"2023-11-14T22:13:20Z","outcome":"queued","passes":[` +
		`{"candidates":[],"outcome":"none","pass":"embedded_tags"},` +
		`{"candidates":[{"confidence":0.25,"evidence":[{"detail":"Kebilegev Bibihi","kind":"title"}],` +
		`"external_id":"rg-1","provider":"musicbrainz:release-group","strategy":"parse"}],"outcome":"ambiguous","pass":"album_match"}]}]}`
	if got := strings.TrimSpace(recorder.Body.String()); got != want {
		t.Errorf("body = %s\nwant %s", got, want)
	}
}

// The same ADR-27 guarantee as resolving: another category's file is not even known to exist.
func TestListIdentificationAttempts_UnreadableRootFolderAnswers404(t *testing.T) {
	fixture := newWorksFixture(t)
	_, hidden := fixture.createCategoryWithRoot(t, "Private Stash", "/media/private")
	fixture.unidentifiedResolver.entry = apiserver.UnidentifiedRow{RootFolderID: hidden.ID}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/library/unidentified/1/attempts"))
	if recorder.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", recorder.Code)
	}
	if fixture.unidentifiedResolver.attemptCalls != 0 {
		t.Error("the attempts of an unreadable file must not even be read")
	}
}
