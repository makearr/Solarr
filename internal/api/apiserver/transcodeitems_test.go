package apiserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeTranscodePipeline serves canned items by id and records what it was asked.
type fakeTranscodePipeline struct {
	items       []apiserver.TranscodeItemView
	lastItems   apiserver.TranscodeItemsQuery
	lastRetry   apiserver.TranscodeRetryQuery
	retryCount  int64
	retryErr    error
	restoreErr  error
	restored    []int64
	resumedKind string
}

func (f *fakeTranscodePipeline) Summary(context.Context) (apiserver.TranscodeSummaryView, error) {
	return apiserver.TranscodeSummaryView{
		Enabled: true, Totals: apiserver.TranscodeSavingsView{Files: 2, OriginalBytes: 1000, ResultBytes: 400},
		Counts:         apiserver.TranscodeCountsView{Needed: 3, Done: 2},
		Libraries:      []apiserver.TranscodeLibraryView{{CategoryID: 4, Name: "Songs", TargetID: 35, Enabled: true, Warnings: []settings.TranscodeWarning{{Code: settings.WarnChecksOff}}}},
		Health:         []apiserver.TranscodeHealthView{{Kind: "video", Mode: "ok"}, {Kind: "audio", Mode: "backoff", ConsecutiveFailures: 3, LastError: "boom"}},
		OriginalsBytes: 600,
	}, nil
}

func (f *fakeTranscodePipeline) Items(_ context.Context, query apiserver.TranscodeItemsQuery) ([]apiserver.TranscodeItemView, error) {
	f.lastItems = query
	var out []apiserver.TranscodeItemView
	for _, item := range f.items {
		if item.MediaObjectID > query.AfterID && int64(len(out)) < query.Size {
			out = append(out, item)
		}
	}
	return out, nil
}

func (f *fakeTranscodePipeline) Item(_ context.Context, id int64) (apiserver.TranscodeItemView, bool, error) {
	for _, item := range f.items {
		if item.MediaObjectID == id {
			return item, true, nil
		}
	}
	return apiserver.TranscodeItemView{}, false, nil
}

func (f *fakeTranscodePipeline) Retry(_ context.Context, query apiserver.TranscodeRetryQuery) (int64, error) {
	f.lastRetry = query
	return f.retryCount, f.retryErr
}

func (f *fakeTranscodePipeline) Restore(_ context.Context, id int64) error {
	f.restored = append(f.restored, id)
	return f.restoreErr
}

func (f *fakeTranscodePipeline) ResumeHealth(_ context.Context, kind string) error {
	f.resumedKind = kind
	return nil
}

func transcodeItemsFixture(t *testing.T) (worksFixture, []*http.Cookie) {
	t.Helper()
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	for id := int64(1); id <= 3; id++ {
		fixture.transcodeItems.items = append(fixture.transcodeItems.items, apiserver.TranscodeItemView{
			MediaObjectID: id, CategoryID: 4, Path: "Album/t.flac", Kind: "audio", State: "done", Lane: "audio",
			ReasonsJSON: `[{"code":"audio_codec","stream":0,"want":"aac","have":"flac"}]`, PlanJSON: `{"container":"m4a"}`, Restorable: true, FinishedAt: 100,
		})
	}
	return fixture, fixture.signIn(t, "admin")
}

func TestTranscodeItems_AreAdministratorOnly(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "listener", settings.RoleReadonly)
	cookies := fixture.signIn(t, "listener")
	for _, request := range []*http.Request{
		authedGet(t, cookies, "/api/v1/transcode/summary"),
		authedGet(t, cookies, "/api/v1/transcode/items"),
		authedGet(t, cookies, "/api/v1/transcode/items/1"),
		authedPost(t, cookies, "/api/v1/transcode/items/retry", `{}`),
		authedPost(t, cookies, "/api/v1/transcode/items/1/restore", ``),
		authedPost(t, cookies, "/api/v1/transcode/health/video/resume", ``),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
}

func TestTranscodeItems_SummaryAndPagesAndOneItem(t *testing.T) {
	fixture, cookies := transcodeItemsFixture(t)
	recorder := fixture.do(authedGet(t, cookies, "/api/v1/transcode/summary"))
	var summary gen.TranscodeSummary
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &summary) != nil {
		t.Fatalf("summary = %d %s", recorder.Code, recorder.Body)
	}
	if summary.Totals.SavedBytes != 600 || summary.Totals.SavedPct != 60 || summary.OriginalsBytes != 600 || len(summary.Libraries) != 1 || (*summary.Libraries[0].Warnings)[0].Code != "checks_off" || summary.Health[1].Mode != "backoff" {
		t.Errorf("summary = %+v", summary)
	}

	var first, second gen.TranscodeItemPage
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/transcode/items?limit=2&state=done&kind=audio&category_id=4&q=flac"))
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &first) != nil {
		t.Fatalf("page 1 = %d %s", recorder.Code, recorder.Body)
	}
	if len(first.Items) != 2 || first.NextCursor == nil || fixture.transcodeItems.lastItems.State != "done" || fixture.transcodeItems.lastItems.Search != "flac" || fixture.transcodeItems.lastItems.CategoryID != 4 {
		t.Fatalf("page 1 = %+v, query %+v", first, fixture.transcodeItems.lastItems)
	}
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/transcode/items?limit=2&cursor="+*first.NextCursor))
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil || len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].MediaObjectId != 3 {
		t.Errorf("page 2 = %+v, %v; want the third item alone and no next page", second, err)
	}
	if first.Items[0].Reasons[0].Have == nil || *first.Items[0].Reasons[0].Have != "flac" || !first.Items[0].Restorable {
		t.Errorf("item = %+v, want the structured reason", first.Items[0])
	}

	var one gen.TranscodeItem
	recorder = fixture.do(authedGet(t, cookies, "/api/v1/transcode/items/2"))
	if err := json.Unmarshal(recorder.Body.Bytes(), &one); err != nil || one.MediaObjectId != 2 || one.Plan == nil || (*one.Plan)["container"] != "m4a" {
		t.Errorf("item = %+v, %v; want the plan with it", one, err)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/transcode/items/99")); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown item = %d, want 404", recorder.Code)
	}
	if recorder := fixture.do(authedGet(t, cookies, "/api/v1/transcode/items?cursor=abc")); recorder.Code != http.StatusBadRequest {
		t.Errorf("a cursor nobody gave = %d, want 400", recorder.Code)
	}
}

func TestTranscodeItems_RetryPassesTheSelectionAndPreviewAndRefusesABadOverride(t *testing.T) {
	fixture, cookies := transcodeItemsFixture(t)
	fixture.transcodeItems.retryCount = 7
	body := `{"state":"failed","category_id":4,"q":"x","excluded_ids":[5],"override":{"quality":24,"preset":"slow","engine":"vaapi","video_bitrate_kbps":900},"preview":true}`
	recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/items/retry", body))
	var result gen.TranscodeRetryResult
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &result) != nil || result.Count != 7 || result.Applied {
		t.Fatalf("retry = %d %s", recorder.Code, recorder.Body)
	}
	got := fixture.transcodeItems.lastRetry
	if !got.Preview || got.State != "failed" || got.CategoryID != 4 || got.Search != "x" || len(got.Excluded) != 1 || got.Quality != 24 || got.Preset != "slow" || got.Engine != "vaapi" || got.VideoBitrateKbps != 900 {
		t.Errorf("retry query = %+v", got)
	}
	fixture.transcodeItems.retryErr = apiserver.ErrTranscodeBadRetry
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/items/retry", `{"ids":[1]}`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("bad override = %d, want 400", recorder.Code)
	}
}

func TestTranscodeItems_RestoreAnswersEachRefusalAsAProblem(t *testing.T) {
	fixture, cookies := transcodeItemsFixture(t)
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/items/1/restore", ``)); recorder.Code != http.StatusOK || len(fixture.transcodeItems.restored) != 1 {
		t.Fatalf("restore = %d %s", recorder.Code, recorder.Body)
	}
	for err, want := range map[error]int{
		apiserver.ErrTranscodeOriginalGone: http.StatusConflict, apiserver.ErrTranscodeNotRestorable: http.StatusConflict, apiserver.ErrTranscodePlaceTaken: http.StatusConflict,
		apiserver.ErrTranscodeNoItem: http.StatusNotFound, apiserver.ErrTranscodeFolder: http.StatusServiceUnavailable,
	} {
		fixture.transcodeItems.restoreErr = err
		recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/items/1/restore", ``))
		if recorder.Code != want || !strings.Contains(recorder.Body.String(), `"detail"`) {
			t.Errorf("%v = %d %s, want %d with a detail", err, recorder.Code, recorder.Body, want)
		}
	}
}

func TestTranscodeItems_ResumeNamesTheKind(t *testing.T) {
	fixture, cookies := transcodeItemsFixture(t)
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/health/audio/resume", ``)); recorder.Code != http.StatusNoContent || fixture.transcodeItems.resumedKind != "audio" {
		t.Errorf("resume = %d, kind %q", recorder.Code, fixture.transcodeItems.resumedKind)
	}
	if recorder := fixture.do(authedPost(t, cookies, "/api/v1/transcode/health/film/resume", ``)); recorder.Code != http.StatusBadRequest {
		t.Errorf("unknown kind = %d, want 400", recorder.Code)
	}
}
