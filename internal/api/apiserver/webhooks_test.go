package apiserver_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
)

// fakeWebhooks keeps what the handlers asked for; the address it is given never comes back.
type fakeWebhooks struct{ saved []apiserver.WebhookInput }

func (*fakeWebhooks) List(context.Context) ([]apiserver.WebhookInfo, error) {
	return []apiserver.WebhookInfo{{ID: 1, Name: "Chat", Preset: "discord", AddressHint: "hooks.example.org", HasSecret: true, Enabled: true, Pending: 2}}, nil
}
func (f *fakeWebhooks) Save(_ context.Context, id int64, input apiserver.WebhookInput) (apiserver.WebhookInfo, error) {
	f.saved = append(f.saved, input)
	return apiserver.WebhookInfo{ID: max(id, 1), Name: input.Name, Preset: input.Preset, AddressHint: "hooks.example.org", HasSecret: input.Secret != ""}, nil
}
func (*fakeWebhooks) Delete(context.Context, int64) error { return nil }
func (*fakeWebhooks) Test(context.Context, int64) (apiserver.WebhookTest, error) {
	return apiserver.WebhookTest{OK: true, Status: 200, Reply: "ok"}, nil
}
func (*fakeWebhooks) Pending(_ context.Context, id int64) ([]apiserver.WebhookWaiting, error) {
	if id != 1 {
		return nil, settings.ErrNotFound
	}
	at := time.Unix(1790000000, 0).UTC()
	return []apiserver.WebhookWaiting{{ID: 5, Event: "imported", Attempts: 2, QueuedAt: at, NextAt: at.Add(time.Minute)}}, nil
}
func (*fakeWebhooks) Vocabulary() apiserver.WebhookVocabulary {
	return apiserver.WebhookVocabulary{Events: []apiserver.WebhookEventView{{Key: "imported", Group: "library"}}, PresetKeys: []string{"generic"}, PresetNames: []string{"Generic JSON"}, PresetNotes: []string{"x"}, PresetSamples: []string{"{}"}}
}

func TestWebhooks_TheAddressAndSecretAreWriteOnlyAndAMemberIsRefused(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	fixture.createUser(t, "member", settings.RoleUser)
	admin, member := fixture.signIn(t, "admin"), fixture.signIn(t, "member")

	recorder := fixture.do(authedPost(t, admin, "/api/v1/webhooks", `{"name":"Chat","preset":"discord","address":"https://hooks.example.org/secret-token","secret":"s3cret","events":["imported"]}`))
	if recorder.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", recorder.Code, recorder.Body)
	}
	for _, leaked := range []string{"secret-token", "s3cret"} {
		if strings.Contains(recorder.Body.String(), leaked) {
			t.Errorf("the response repeats %q", leaked)
		}
	}
	if !strings.Contains(recorder.Body.String(), `"address_hint":"hooks.example.org"`) || !strings.Contains(recorder.Body.String(), `"has_secret":true`) {
		t.Errorf("response = %s", recorder.Body)
	}
	if recorder := fixture.do(authedGet(t, admin, "/api/v1/webhooks")); !strings.Contains(recorder.Body.String(), `"pending":2`) {
		t.Errorf("list = %s", recorder.Body)
	}
	for _, request := range []*http.Request{
		authedGet(t, member, "/api/v1/webhooks"),
		authedPost(t, member, "/api/v1/webhooks", `{"name":"x","preset":"generic","events":[]}`),
		authedPost(t, member, "/api/v1/webhooks/1/test", `{}`),
		authedDelete(t, member, "/api/v1/webhooks/1"),
		authedGet(t, member, "/api/v1/webhooks/events"),
		authedGet(t, member, "/api/v1/webhooks/1/pending"),
	} {
		if recorder := fixture.do(request); recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s by a member = %d, want 403", request.Method, request.URL.Path, recorder.Code)
		}
	}
	if len(fixture.webhooks.saved) != 1 {
		t.Errorf("saved %d times, want once (the member's add must not reach the store)", len(fixture.webhooks.saved))
	}
}

func TestWebhooks_PendingListsTheWaitingDeliveriesWithoutAPayload(t *testing.T) {
	fixture := newWorksFixture(t)
	fixture.createUser(t, "admin", settings.RoleAdmin)
	admin := fixture.signIn(t, "admin")
	recorder := fixture.do(authedGet(t, admin, "/api/v1/webhooks/1/pending"))
	want := `[{"attempts":2,"event":"imported","id":5,"next_at":"2026-09-21T14:14:20Z","queued_at":"2026-09-21T14:13:20Z"}]`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Errorf("pending = %d %s, want %s", recorder.Code, recorder.Body, want)
	}
	if recorder := fixture.do(authedGet(t, admin, "/api/v1/webhooks/9/pending")); recorder.Code != http.StatusNotFound {
		t.Errorf("unknown webhook = %d, want 404", recorder.Code)
	}
}
