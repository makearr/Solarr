package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeHooks struct {
	hooks   []settings.Webhook
	results map[int64]int
	off     map[int64]bool
}

func (f *fakeHooks) Webhooks(context.Context) ([]settings.Webhook, error) { return f.hooks, nil }
func (f *fakeHooks) WebhookByID(_ context.Context, id int64) (settings.Webhook, error) {
	for _, hook := range f.hooks {
		if hook.ID == id {
			return hook, nil
		}
	}
	return settings.Webhook{}, settings.ErrNotFound
}
func (f *fakeHooks) SetWebhookResult(_ context.Context, id int64, status int, _ string, _ int64) error {
	f.results[id] = status
	return nil
}
func (f *fakeHooks) SetWebhookEnabled(_ context.Context, id int64, enabled bool) error {
	f.off[id] = !enabled
	return nil
}

type fakeOutbox struct {
	rows    []library.WebhookDelivery
	retried map[int64]int64
	done    []int64
}

func (f *fakeOutbox) Add(_ context.Context, rows []library.WebhookDelivery) error {
	f.rows = append(f.rows, rows...)
	return nil
}
func (f *fakeOutbox) Due(context.Context, int64, int64) ([]library.WebhookDelivery, error) {
	return f.rows, nil
}
func (f *fakeOutbox) Done(_ context.Context, id int64) error { f.done = append(f.done, id); return nil }
func (f *fakeOutbox) Retry(_ context.Context, id, next int64) error {
	f.retried[id] = next
	return nil
}

type fakeDoer struct {
	status  int
	request *http.Request
}

func (f *fakeDoer) Do(_ context.Context, _ settings.Webhook, request *http.Request) (*http.Response, error) {
	f.request = request
	return &http.Response{StatusCode: f.status, Body: io.NopCloser(strings.NewReader("reply"))}, nil
}

var epoch = time.Unix(1_800_000_000, 0)

func newService(hooks ...settings.Webhook) (Service, *fakeHooks, *fakeOutbox, *fakeDoer) {
	h := &fakeHooks{hooks: hooks, results: map[int64]int{}, off: map[int64]bool{}}
	o := &fakeOutbox{retried: map[int64]int64{}}
	d := &fakeDoer{status: 200}
	return Service{Hooks: h, Outbox: o, Doer: d, Clock: clock.NewFake(epoch), Instance: Instance{Name: "Solarr", Version: "test"}}, h, o, d
}

func hook(id int64, events []string, libraries []int64, adult bool) settings.Webhook {
	return settings.Webhook{ID: id, Enabled: true, Preset: "generic", URL: "https://hooks.example.org/x", Events: events, CategoryIDs: libraries, IncludeAdult: adult}
}

func TestRecordQueuesOnlyForWebhooksThatTakeTheEventTheLibraryAndTheAdultGate(t *testing.T) {
	disabled := hook(5, []string{"imported"}, nil, true)
	disabled.Enabled = false
	service, _, outbox, _ := newService(
		hook(1, []string{"imported"}, nil, false),
		hook(2, []string{"grabbed"}, nil, false),
		hook(3, []string{"imported"}, []int64{9}, false),
		hook(4, []string{"imported"}, []int64{7}, true),
		disabled,
	)
	if err := service.Record(t.Context(), Occurrence{Event: EventImported, Library: Library{ID: 7}}); err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, row := range outbox.rows {
		got = append(got, row.WebhookID)
	}
	if want := []int64{1, 4}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("queued for %v, want %v", got, want)
	}
	outbox.rows = nil
	if err := service.Record(t.Context(), Occurrence{Event: EventImported, Library: Library{ID: 7}, Adult: true}); err != nil {
		t.Fatal(err)
	}
	if len(outbox.rows) != 1 || outbox.rows[0].WebhookID != 4 {
		t.Fatalf("an adult library's event reached %+v, want only the webhook that includes adult libraries", outbox.rows)
	}
}

func TestRecordLeavesFilePathsOutUnlessTheWebhookAsksForThem(t *testing.T) {
	asking := hook(2, []string{"imported"}, nil, false)
	asking.IncludePaths = true
	service, _, outbox, _ := newService(hook(1, []string{"imported"}, nil, false), asking)
	files := []File{{Name: "a.cbz", Path: "/library/a.cbz"}}
	if err := service.Record(t.Context(), Occurrence{Event: EventImported, Files: files}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(outbox.rows[0].Payload, "/library") || !strings.Contains(outbox.rows[1].Payload, "/library/a.cbz") {
		t.Fatalf("payloads = %s | %s", outbox.rows[0].Payload, outbox.rows[1].Payload)
	}
}

func TestDeliverRemovesOnSuccessBacksOffOnFailureDropsAtTheEighthAndSwitchesOffOn410(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		attempts int64
		done     bool
		retryAt  int64
		off      bool
	}{
		{"delivered", 204, 0, true, 0, false},
		{"first failure waits a minute", 500, 0, false, epoch.Add(time.Minute).Unix(), false},
		{"fourth failure waits two hours", 502, 3, false, epoch.Add(2 * time.Hour).Unix(), false},
		{"eighth failure drops the row", 500, 7, true, 0, false},
		{"gone switches the webhook off", 410, 0, true, 0, true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			service, hooks, outbox, doer := newService(hook(1, []string{"imported"}, nil, false))
			doer.status = one.status
			outbox.rows = []library.WebhookDelivery{{ID: 42, WebhookID: 1, Event: "imported", Payload: `{"schema":1,"event":"imported"}`, Attempts: one.attempts}}
			if err := service.Deliver(t.Context()); err != nil {
				t.Fatal(err)
			}
			if done := len(outbox.done) == 1; done != one.done {
				t.Errorf("done = %v, want %v", done, one.done)
			}
			if outbox.retried[42] != one.retryAt {
				t.Errorf("retry at %d, want %d", outbox.retried[42], one.retryAt)
			}
			if hooks.off[1] != one.off || hooks.results[1] != one.status {
				t.Errorf("off=%v status=%d", hooks.off[1], hooks.results[1])
			}
			if got := doer.request.Header.Get("X-Solarr-Delivery"); got != "42" {
				t.Errorf("delivery header = %q", got)
			}
		})
	}
}

func TestSignatureMatchesAKnownHMACSHA256Vector(t *testing.T) {
	got := Signature("key", []byte("The quick brown fox jumps over the lazy dog"))
	if want := "sha256=f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"; got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

func TestEveryPresetMakesValidBodyWhenTheTitleHasQuotesAndANewline(t *testing.T) {
	payload := Payload{Schema: 1, Event: EventImported, Work: &Work{Title: "A \"quoted\"\nline"}}
	for _, preset := range Presets() {
		body, err := preset.Render(payload)
		if err != nil {
			t.Fatalf("%s: %v", preset.Key, err)
		}
		if strings.HasPrefix(preset.ContentType, "application/json") && !json.Valid(body) {
			t.Errorf("%s body is not valid JSON: %s", preset.Key, body)
		}
	}
	if len(Presets()) != 6 {
		t.Errorf("%d presets, want generic, discord, slack, ntfy, gotify and apprise", len(Presets()))
	}
}

func TestADisabledOrGoneWebhooksRowsAreDroppedNotSent(t *testing.T) {
	service, _, outbox, doer := newService()
	outbox.rows = []library.WebhookDelivery{{ID: 3, WebhookID: 99, Payload: `{}`}}
	if err := service.Deliver(t.Context()); err != nil {
		t.Fatal(err)
	}
	if doer.request != nil || len(outbox.done) != 1 {
		t.Fatalf("a row of a webhook that is gone was sent or kept: %v", outbox.done)
	}
}

func TestAnInstanceEventReachesAWebhookLimitedToLibraries(t *testing.T) {
	service, _, outbox, _ := newService(hook(1, []string{"health_raised"}, []int64{9}, false))
	if err := service.Record(t.Context(), Occurrence{Event: EventHealthRaised, Message: "a check"}); err != nil {
		t.Fatal(err)
	}
	if len(outbox.rows) != 1 {
		t.Fatalf("queued %d rows, want 1", len(outbox.rows))
	}
	outbox.rows = nil
	if err := service.Record(t.Context(), Occurrence{Event: EventHealthRaised, Library: Library{ID: 3}}); err != nil {
		t.Fatal(err)
	}
	if len(outbox.rows) != 0 {
		t.Fatalf("a library event of another library queued %d rows", len(outbox.rows))
	}
}

func TestEveryShippedPresetRendersTheSampleAndTheJSONOnesParse(t *testing.T) {
	presets := Presets()
	if len(presets) != 6 {
		t.Fatalf("%d presets, want 6", len(presets))
	}
	for _, preset := range presets {
		sample := preset.Sample()
		if sample == "" || !strings.Contains(sample, "Example Series") {
			t.Errorf("%s: sample = %q", preset.Key, sample)
		}
		if preset.ContentType == "application/json" && !json.Valid([]byte(sample)) {
			t.Errorf("%s: sample is not JSON: %s", preset.Key, sample)
		}
	}
}
