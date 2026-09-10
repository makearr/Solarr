package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/settings"
)

// fakeCacheManager is apiserver.CacheManager over an in-memory report, so a
// test can assert dispatch and error mapping without a real
// internal/cachebudget.Sweep.
type fakeCacheManager struct {
	report       apiserver.CacheReport
	setBudgetErr error
	clearErr     error
	clearCalls   int
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
