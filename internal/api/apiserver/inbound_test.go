package apiserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
)

// fakeInbound knows one webhook (id 1, token "good-token") that may scan and search; it records what ran.
type fakeInbound struct {
	actions []string
	ran     []apiserver.InboundRequest
}

func (f *fakeInbound) hook() apiserver.InboundInfo {
	actions := f.actions
	if actions == nil {
		actions = []string{"scan", "search"}
	}
	return apiserver.InboundInfo{ID: 1, Name: "Tool", Enabled: true, Actions: actions}
}
func (f *fakeInbound) List(context.Context) ([]apiserver.InboundInfo, error) {
	return []apiserver.InboundInfo{f.hook()}, nil
}
func (f *fakeInbound) Save(_ context.Context, _ int64, _ apiserver.InboundInput) (apiserver.InboundCreated, error) {
	return apiserver.InboundCreated{Info: f.hook(), Token: "good-token"}, nil
}
func (f *fakeInbound) Rotate(context.Context, int64) (apiserver.InboundCreated, error) {
	return apiserver.InboundCreated{Info: f.hook(), Token: "next-token"}, nil
}
func (*fakeInbound) Delete(context.Context, int64) error { return nil }
func (f *fakeInbound) Authenticate(_ context.Context, id int64, token string) (apiserver.InboundInfo, bool) {
	if id != 1 || token != "good-token" {
		return apiserver.InboundInfo{}, false
	}
	return f.hook(), true
}
func (f *fakeInbound) Run(_ context.Context, _ apiserver.InboundInfo, request apiserver.InboundRequest) (apiserver.InboundResult, error) {
	f.ran = append(f.ran, request)
	return apiserver.InboundResult{JobID: 42}, nil
}

func hookCall(target, token, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return request
}

func TestInboundWebhook_AnActionEnqueuesAndAnswers202(t *testing.T) {
	fixture := newWorksFixture(t)
	recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", `{"action":"scan","library_id":5}`))
	if recorder.Code != http.StatusAccepted || !strings.Contains(recorder.Body.String(), `"job_id":42`) {
		t.Fatalf("scan = %d %s", recorder.Code, recorder.Body)
	}
	if len(fixture.inbound.ran) != 1 || fixture.inbound.ran[0].Action != "scan" || fixture.inbound.ran[0].LibraryID != 5 {
		t.Fatalf("ran %+v", fixture.inbound.ran)
	}
	// The token in the query and the X-Solarr-Token header serve a sender that cannot set Authorization.
	query := httptest.NewRequest(http.MethodPost, "/api/v1/hooks/1?token=good-token&action=search", strings.NewReader(`{"work_id":3}`))
	header := httptest.NewRequest(http.MethodPost, "/api/v1/hooks/1", strings.NewReader(`{"action":"search"}`))
	header.Header.Set("X-Solarr-Token", "good-token")
	for _, request := range []*http.Request{query, header} {
		if recorder := fixture.do(request); recorder.Code != http.StatusAccepted {
			t.Errorf("%s = %d %s", request.URL, recorder.Code, recorder.Body)
		}
	}
}

func TestInboundWebhook_AWrongTokenAnUnknownIdAndAWordNotANumberAnswerTheSame404(t *testing.T) {
	fixture := newWorksFixture(t)
	var bodies []string
	for _, request := range []*http.Request{
		hookCall("/api/v1/hooks/1", "bad-token", `{"action":"scan"}`),
		hookCall("/api/v1/hooks/1", "", `{"action":"scan"}`),
		hookCall("/api/v1/hooks/99", "good-token", `{"action":"scan"}`),
		hookCall("/api/v1/hooks/abc", "good-token", `{"action":"scan"}`),
	} {
		recorder := fixture.do(request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", request.URL, recorder.Code)
		}
		bodies = append(bodies, recorder.Body.String())
	}
	for _, body := range bodies[1:] {
		if body != bodies[0] {
			t.Errorf("answers differ: %q vs %q", body, bodies[0])
		}
	}
	if len(fixture.inbound.ran) != 0 {
		t.Errorf("an action ran without a valid token")
	}
}

func TestInboundWebhook_AnActionOutsideTheTokensListIs403AndABodyOverTheLimitIs413(t *testing.T) {
	fixture := newWorksFixture(t)
	if recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", `{"action":"import","path":"/x"}`)); recorder.Code != http.StatusForbidden {
		t.Errorf("import = %d, want 403", recorder.Code)
	}
	big := `{"action":"scan","path":"` + strings.Repeat("a", 70<<10) + `"}`
	if recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", big)); recorder.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("big body = %d, want 413", recorder.Code)
	}
	if recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", `[1,2]`)); recorder.Code != http.StatusBadRequest {
		t.Errorf("array body = %d, want 400", recorder.Code)
	}
	if len(fixture.inbound.ran) != 0 {
		t.Errorf("ran %+v", fixture.inbound.ran)
	}
}

func TestInboundWebhook_ATokenThatMayDoOneThingNeedsNoActionAndSixtyCallsAMinuteAreAllowed(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.inbound.actions = []string{"playback"}
	for call := 1; call <= 60; call++ {
		if recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", `{"ItemId":"x","Id":7}`)); recorder.Code != http.StatusAccepted {
			t.Fatalf("call %d = %d %s", call, recorder.Code, recorder.Body)
		}
	}
	recorder := fixture.do(hookCall("/api/v1/hooks/1", "good-token", `{}`))
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("call 61 = %d (Retry-After %q), want 429", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}
