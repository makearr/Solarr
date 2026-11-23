package apiserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeDefinitions keeps what the handlers asked of the definition administration.
type fakeDefinitions struct {
	saved   []string
	removed []string
	enabled []string
}

func (*fakeDefinitions) List(context.Context, string) ([]apiserver.DefinitionInfo, error) {
	return []apiserver.DefinitionInfo{{Kind: "metadata", Key: "alpha", Name: "Alpha", Engine: "http", Origin: "custom", State: "needs_approval", Hosts: []string{"api.example.org"}}}, nil
}
func (*fakeDefinitions) Text(context.Context, string, string) (apiserver.DefinitionText, error) {
	return apiserver.DefinitionText{Text: "schema: 1\n"}, nil
}
func (f *fakeDefinitions) Put(_ context.Context, _, key, text string, dryRun bool) (bool, []apiserver.DefinitionProblem, error) {
	if strings.Contains(text, "broken") {
		return false, []apiserver.DefinitionProblem{{Severity: "error", Code: "unknown_field", Line: 3}}, nil
	}
	if !dryRun {
		f.saved = append(f.saved, key)
	}
	return !dryRun, nil, nil
}
func (f *fakeDefinitions) Remove(_ context.Context, _, key string) error {
	f.removed = append(f.removed, key)
	return nil
}
func (f *fakeDefinitions) SetEnabled(_ context.Context, _, key string, enabled bool) error {
	f.enabled = append(f.enabled, key+map[bool]string{true: ":on", false: ":off"}[enabled])
	return nil
}
func (*fakeDefinitions) SetSettings(context.Context, string, string, map[string]string) error {
	return nil
}
func (*fakeDefinitions) Test(context.Context, string, string, string, int) (apiserver.DefinitionTest, error) {
	return apiserver.DefinitionTest{OK: true}, nil
}
func (*fakeDefinitions) Reload(context.Context) error { return nil }

func TestProviderDefinitions_AFileWithAnErrorIsNeverSavedAndAMemberIsRefused(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "member", settings.RoleUser)
	admin, member := fixture.signIn(t, "admin"), fixture.signIn(t, "member")

	recorder := fixture.do(authedPut(t, admin, "/api/v1/provider-definitions/metadata/alpha", `{"text":"broken: true"}`))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"saved":false`) || !strings.Contains(recorder.Body.String(), `"unknown_field"`) {
		t.Fatalf("broken put = %d %s", recorder.Code, recorder.Body)
	}
	recorder = fixture.do(authedPut(t, admin, "/api/v1/provider-definitions/metadata/alpha?dry_run=true", `{"text":"schema: 1"}`))
	if recorder.Code != http.StatusOK || len(fixture.definitions.saved) != 0 {
		t.Fatalf("dry run = %d, saved %v", recorder.Code, fixture.definitions.saved)
	}
	recorder = fixture.do(authedPut(t, admin, "/api/v1/provider-definitions/metadata/alpha", `{"text":"schema: 1"}`))
	if recorder.Code != http.StatusOK || len(fixture.definitions.saved) != 1 {
		t.Fatalf("put = %d, saved %v", recorder.Code, fixture.definitions.saved)
	}
	for _, request := range []*http.Request{
		authedGet(t, member, "/api/v1/provider-definitions?kind=metadata"),
		authedPut(t, member, "/api/v1/provider-definitions/metadata/alpha", `{"text":"x"}`),
		authedDelete(t, member, "/api/v1/provider-definitions/metadata/alpha"),
		authedPost(t, member, "/api/v1/provider-definitions/metadata/alpha/enable", `{}`),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s by a member = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
	if len(fixture.definitions.removed) != 0 || len(fixture.definitions.enabled) != 0 {
		t.Errorf("a member changed something: %v %v", fixture.definitions.removed, fixture.definitions.enabled)
	}
	if recorder := fixture.do(authedPost(t, admin, "/api/v1/provider-definitions/metadata/alpha/enable", `{}`)); recorder.Code != http.StatusNoContent || fixture.definitions.enabled[0] != "alpha:on" {
		t.Errorf("enable = %d %v", recorder.Code, fixture.definitions.enabled)
	}
}
