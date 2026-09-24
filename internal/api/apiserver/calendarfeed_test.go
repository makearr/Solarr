package apiserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func feedToken(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Token *string `json:"token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal %s: %v", recorder.Body, err)
	}
	if body.Token == nil {
		return ""
	}
	return *body.Token
}

func feedRequest(token string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/v1/calendar/feed.ics?token="+url.QueryEscape(token), nil)
}

// A calendar client cannot log in, so the token is the credential: it shows that user's
// libraries only, shows again until regenerated, and a revoked one stops at once.
func TestCalendarFeed_TokenLifecycle(t *testing.T) {
	fixture := newWorksFixture(t)
	cipher, err := secrets.New(testArrCipherKey())
	if err != nil {
		t.Fatal(err)
	}
	fixture.cache.EnableSecrets(cipher)
	visible, _ := fixture.cache.CreateCategory(t.Context(), "music", "Music", 10)
	if _, err := fixture.cache.CreateCategory(t.Context(), "music", "Hidden", 20); err != nil {
		t.Fatal(err)
	}
	user := fixture.createUser(t, "listener", settings.RoleReadonly)
	if err := fixture.cache.SetCategoryAccess(t.Context(), user.ID, visible.ID, settings.PermissionRead); err != nil {
		t.Fatal(err)
	}
	cookies := fixture.signIn(t, "listener")
	fixture.calendar.rows = []apiserver.CalendarRow{
		{WorkID: 7, Title: "Tender Prey", Artist: "Nick Cave", ReleaseDate: "2026-06-01"},
	}

	if token := feedToken(t, fixture.do(authedGet(t, cookies, "/api/v1/me/calendar-feed"))); token != "" {
		t.Fatalf("a new user already has a feed: %q", token)
	}
	token := feedToken(t, fixture.do(authedPost(t, cookies, "/api/v1/me/calendar-feed", "")))
	if !strings.HasPrefix(token, "cal_") {
		t.Fatalf("token = %q, want a cal_ token", token)
	}
	if again := feedToken(t, fixture.do(authedGet(t, cookies, "/api/v1/me/calendar-feed"))); again != token {
		t.Errorf("the link shown again is %q, want %q", again, token)
	}

	feed := fixture.do(feedRequest(token))
	if feed.Code != http.StatusOK || !strings.HasPrefix(feed.Header().Get("Content-Type"), "text/calendar") {
		t.Fatalf("feed: status %d, type %q, %s", feed.Code, feed.Header().Get("Content-Type"), feed.Body)
	}
	if !strings.Contains(feed.Body.String(), "SUMMARY:Nick Cave - Tender Prey\r\n") {
		t.Errorf("feed lacks the release:\n%s", feed.Body)
	}
	if want := []int64{visible.ID}; !reflect.DeepEqual(fixture.calendar.entriesCategories, want) {
		t.Errorf("feed scoped to %v, want %v", fixture.calendar.entriesCategories, want)
	}

	regenerated := feedToken(t, fixture.do(authedPost(t, cookies, "/api/v1/me/calendar-feed", "")))
	if code := fixture.do(feedRequest(token)).Code; code != http.StatusNotFound {
		t.Errorf("the replaced token answers %d, want 404", code)
	}
	if code := fixture.do(authedDelete(t, cookies, "/api/v1/me/calendar-feed")).Code; code != http.StatusNoContent {
		t.Fatalf("delete: status %d", code)
	}
	if code := fixture.do(feedRequest(regenerated)).Code; code != http.StatusNotFound {
		t.Errorf("a revoked token answers %d, want 404", code)
	}
	if code := fixture.do(feedRequest("not-a-token")).Code; code != http.StatusNotFound {
		t.Errorf("a malformed token answers %d, want 404", code)
	}
}
