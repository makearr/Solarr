package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// fakeCacheManager is apiserver.CacheManager over an in-memory report, so a
// test can assert dispatch and error mapping without a real
// internal/jobs/cachebudget.Sweep.
type fakeCacheManager struct {
	report       apiserver.CacheReport
	setBudgetErr error
	clearErr     error
	clearCalls   int
	shrunkFor    time.Duration
	lastBudget   apiserver.CacheBudget
}

func (f *fakeCacheManager) Report(context.Context) (apiserver.CacheReport, error) {
	return f.report, nil
}

func (f *fakeCacheManager) SetBudget(_ context.Context, budget apiserver.CacheBudget) error {
	if f.setBudgetErr != nil {
		return f.setBudgetErr
	}
	f.lastBudget = budget
	f.report.Budget = budget
	return nil
}

func (f *fakeCacheManager) Shrink(_ context.Context, unusedFor time.Duration) error {
	f.shrunkFor = unusedFor
	return nil
}

func (f *fakeCacheManager) Clear(context.Context) error {
	f.clearCalls++
	if f.clearErr != nil {
		return f.clearErr
	}
	zero := int64(0)
	f.report.ImagesBytes, f.report.MetadataBytes = &zero, &zero
	return nil
}

type cacheReportBody struct {
	MaxBytes             int64  `json:"max_bytes"`
	MaxAgeDays           int64  `json:"max_age_days"`
	ImagesSharePercent   int64  `json:"images_share_percent"`
	MetadataSharePercent int64  `json:"metadata_share_percent"`
	ImagesBytes          *int64 `json:"images_bytes"`
	MetadataBytes        *int64 `json:"metadata_bytes"`
}

func TestGetCache_ReturnsTheCurrentReport(t *testing.T) {
	fixture := newWorksFixture(t)
	imagesBytes, metadataBytes := int64(100), int64(200)
	fixture.cacheManager.report = apiserver.CacheReport{
		Budget: apiserver.CacheBudget{
			MaxBytes: settings.DefaultCacheMaxBytes, MaxAgeDays: 90, ImagesSharePercent: 80, MetadataSharePercent: 20,
		},
		ImagesBytes: &imagesBytes, MetadataBytes: &metadataBytes,
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedGet(t, cookies, "/api/v1/cache"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body cacheReportBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.MaxAgeDays != 90 || body.ImagesBytes == nil || *body.ImagesBytes != 100 {
		t.Errorf("body = %+v", body)
	}
}

func TestGetCache_RequiresAuthentication(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(authedGet(t, nil, "/api/v1/cache"))
	if recorder.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", recorder.Code)
	}
}

func TestUpdateCacheBudget_RoundTrips(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/cache",
		`{"max_bytes":5000000000,"max_age_days":30,"images_share_percent":60,"metadata_share_percent":40}`))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	var body cacheReportBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.MaxBytes != 5_000_000_000 || body.ImagesSharePercent != 60 || body.MetadataSharePercent != 40 {
		t.Errorf("body = %+v", body)
	}
}

func TestUpdateCacheBudget_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/cache",
		`{"max_bytes":1,"max_age_days":1,"images_share_percent":80,"metadata_share_percent":20}`))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
}

func TestUpdateCacheBudget_MapsValidationProblemsTo400(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.cacheManager.setBudgetErr = config.Problems{{Path: "namespace_share", Expectation: "must sum to 100"}}
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPatch(t, cookies, "/api/v1/cache",
		`{"max_bytes":1,"max_age_days":1,"images_share_percent":80,"metadata_share_percent":80}`))
	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (%s)", recorder.Code, recorder.Body)
	}
}

func TestClearCache_RemovesEverythingAndAnswers200(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	cookies := fixture.signIn(t, "admin")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/cache/clear", ``))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.cacheManager.clearCalls != 1 {
		t.Errorf("clearCalls = %d, want 1", fixture.cacheManager.clearCalls)
	}
	var body cacheReportBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ImagesBytes == nil || *body.ImagesBytes != 0 {
		t.Errorf("body = %+v, want images_bytes 0 after clear", body)
	}
}

func TestClearCache_RequiresAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")

	recorder := fixture.do(authedPost(t, cookies, "/api/v1/cache/clear", ``))
	if recorder.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", recorder.Code)
	}
	if fixture.cacheManager.clearCalls != 0 {
		t.Error("clear ran despite the caller not being an administrator")
	}
}

func TestShrinkCache_AsksForAWeekUnusedAndNeedsAnAdministrator(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	listener := fixture.signIn(t, "listener")
	if recorder := fixture.do(authedPost(t, listener, "/api/v1/cache/shrink", "")); recorder.Code != http.StatusForbidden {
		t.Fatalf("a listener's status = %d, want 403", recorder.Code)
	}
	fixture.createUser(t, "boss", settings.RoleAdmin)
	admin := fixture.signIn(t, "boss")
	if recorder := fixture.do(authedPost(t, admin, "/api/v1/cache/shrink", "")); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if fixture.cacheManager.shrunkFor != 7*24*time.Hour {
		t.Errorf("shrunk for %v, want 168h", fixture.cacheManager.shrunkFor)
	}
}

type fakeMemoryCopies struct {
	err     error
	changes []apiserver.MemoryCopySettings
}

func (f *fakeMemoryCopies) Status(context.Context) (apiserver.MemoryCopyStatus, error) {
	return apiserver.MemoryCopyStatus{TmpfsDir: "/dev/shm/solarr"}, nil
}

func (f *fakeMemoryCopies) Set(_ context.Context, change apiserver.MemoryCopySettings) error {
	f.changes = append(f.changes, change)
	return f.err
}

func TestSetMemoryCopies_MapsTheTwoRefusals(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "boss", settings.RoleAdmin)
	admin := fixture.signIn(t, "boss")
	body := `{"cache_enabled":true,"database_enabled":false,"cache_sync_minutes":10,"database_sync_minutes":5,"acknowledged":false}`
	for _, test := range []struct {
		refusal error
		want    int
	}{{nil, http.StatusOK}, {apiserver.ErrRiskNotAcknowledged, http.StatusBadRequest}, {apiserver.ErrNotEnoughMemory, http.StatusConflict}} {
		fixture.memoryCopies.err = test.refusal
		if recorder := fixture.do(authedPut(t, admin, "/api/v1/cache/memory-copies", body)); recorder.Code != test.want {
			t.Errorf("refusal %v: status = %d, want %d (%s)", test.refusal, recorder.Code, test.want, recorder.Body)
		}
	}
	fixture.createUser(t, "listener", settings.RoleReadonly)
	if recorder := fixture.do(authedPut(t, fixture.signIn(t, "listener"), "/api/v1/cache/memory-copies", body)); recorder.Code != http.StatusForbidden {
		t.Errorf("a listener's status = %d, want 403", recorder.Code)
	}
}
