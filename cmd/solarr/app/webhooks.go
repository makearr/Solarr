package app

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/secrets"
	"github.com/makearr/Solarr/internal/service/webhook"
)

// webhookDeliverJobType is the registered type of one run of the webhook outbox.
const webhookDeliverJobType = jobs.Type("library.webhook_deliver")

// webhookDeliverScheduleCron picks up due rows every minute.
const webhookDeliverScheduleCron = "* * * * *"

// webhookDeliverDefinition is mutating: the dry-run gate refuses it, since a delivery is an outward request.
var webhookDeliverDefinition = jobs.Definition{
	Name: "Sending webhooks", Type: webhookDeliverJobType, Pool: jobs.PoolHousekeeping, Mutating: true, MaxAttempts: 1,
}

type webhookDeliverJob struct {
	service webhook.Service
	p       *process
}

func (j webhookDeliverJob) Run(ctx context.Context, _ jobs.Job) error {
	j.p.watchHealth(ctx)
	return j.service.Deliver(ctx)
}

// webhookService is the outbox over this process's stores.
func (p *process) webhookService() webhook.Service {
	return webhook.Service{
		Hooks: p.cache, Outbox: library.NewWebhookPending(p.library), Doer: webhookDoer{p}, Clock: clock.NewSystem(),
		Instance: webhook.Instance{Name: "Solarr", Version: p.build.Version}, Logger: p.logger,
	}
}

// webhookDoer sends through the guard of the owner's own address: the one host, LAN allowed, and no redirect followed (MaxRedirects 1 allows no hop).
type webhookDoer struct{ p *process }

func (d webhookDoer) Do(ctx context.Context, hook settings.Webhook, request *http.Request) (*http.Response, error) {
	client := d.p.providerClient("webhook:"+strconv.FormatInt(hook.ID, 10), connectionGuard(hook.URL.Reveal()), httpx.ClientConfig{MaxRedirects: 1})
	return client.Do(ctx, request)
}

func (p *process) registerWebhookJob(registry *jobs.Registry, executor *jobs.Executor) error {
	if err := registry.Register(webhookDeliverDefinition); err != nil {
		return err
	}
	executor.Handle(webhookDeliverJobType, webhookDeliverJob{service: p.webhookService(), p: p})
	return nil
}

// enqueueWebhookDelivery asks for a run; one at a time (action key).
func (p *process) enqueueWebhookDelivery(ctx context.Context) (jobs.EnqueueResult, error) {
	return p.queue.Enqueue(ctx, jobs.Request{Type: webhookDeliverJobType, ActionKey: "webhook:deliver", Priority: jobs.PriorityLow})
}

// recordWebhook queues an event of a work for the webhooks that take it, from the seam the media servers are told at.
func (p *process) recordWebhook(ctx context.Context, categoryID, workID int64, event webhook.Event) {
	category, found := p.cache.Snapshot().Category(categoryID)
	if !found {
		return
	}
	occurrence := webhook.Occurrence{
		Event: event, Library: webhook.Library{ID: category.ID, Name: category.Name, MediaType: category.MediaType, Kind: category.Kind},
		Adult: p.categoryAdult(category),
	}
	if titles, err := library.NewMediaServerPending(p.library).WorkTitles(ctx, []int64{workID}); err == nil {
		occurrence.Work = &webhook.Work{ID: workID, Title: titles[workID]}
	}
	if err := p.webhookService().Record(ctx, occurrence); err != nil {
		p.logger.Warn("could not queue a webhook", "event", string(event), "error", err)
	}
}

// categoryAdult says a library's kind is an adult one, or it is the adult type.
func (p *process) categoryAdult(category settings.Category) bool {
	for _, descriptor := range p.media.Descriptors() {
		if string(descriptor.Key) != category.MediaType {
			continue
		}
		for _, kind := range descriptor.Kinds {
			if kind.Key == category.Kind && kind.Adult {
				return true
			}
		}
	}
	return category.MediaType == "adult"
}

// webhookAdmin implements apiserver.Webhooks.
type webhookAdmin struct{ p *process }

var _ apiserver.Webhooks = webhookAdmin{}

func (a webhookAdmin) info(ctx context.Context, hook settings.Webhook) apiserver.WebhookInfo {
	info := apiserver.WebhookInfo{
		ID: hook.ID, Name: hook.Name, Preset: hook.Preset, LastError: hook.LastError, Enabled: hook.Enabled, HasSecret: hook.Secret != "",
		IncludePaths: hook.IncludePaths, IncludeAdult: hook.IncludeAdult, Events: hook.Events, CategoryIDs: hook.CategoryIDs, LastStatus: hook.LastStatus,
	}
	if parsed, err := url.Parse(hook.URL.Reveal()); err == nil {
		info.AddressHint = parsed.Host
	}
	if hook.LastAt > 0 {
		info.LastAt = time.Unix(hook.LastAt, 0)
	}
	if pending, next, err := library.NewWebhookPending(a.p.library).Count(ctx, hook.ID); err == nil {
		info.Pending = pending
		if next > 0 {
			info.NextAttempt = time.Unix(next, 0)
		}
	}
	return info
}

// List implements apiserver.Webhooks.
func (a webhookAdmin) List(ctx context.Context) ([]apiserver.WebhookInfo, error) {
	hooks, err := a.p.cache.Webhooks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.WebhookInfo, len(hooks))
	for index, hook := range hooks {
		out[index] = a.info(ctx, hook)
	}
	return out, nil
}

func validWebhook(input apiserver.WebhookInput, adding bool) config.Problems {
	var problems config.Problems
	bad := func(path, value, expectation string) {
		problems = append(problems, config.Problem{Path: path, Value: value, Expectation: expectation})
	}
	if input.Name == "" {
		bad("name", "", "a name")
	}
	if _, found := webhook.PresetByKey(input.Preset); !found {
		bad("preset", input.Preset, "one of the presets")
	}
	if input.Address != "" || adding {
		if parsed, err := url.Parse(input.Address); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			bad("address", "", "an http or https address")
		}
	}
	for _, event := range input.Events {
		if !webhook.Known(event) {
			bad("events", event, "an event Solarr names")
		}
	}
	return problems
}

// Save implements apiserver.Webhooks.
func (a webhookAdmin) Save(ctx context.Context, id int64, input apiserver.WebhookInput) (apiserver.WebhookInfo, error) {
	if problems := validWebhook(input, id == 0); len(problems) > 0 {
		return apiserver.WebhookInfo{}, problems
	}
	saved, err := a.p.cache.SaveWebhook(ctx, settings.Webhook{
		ID: id, Name: input.Name, Preset: input.Preset, Enabled: input.Enabled, URL: secretOf(input.Address), Secret: secretOf(input.Secret),
		Events: slices.Clone(input.Events), CategoryIDs: input.CategoryIDs, IncludePaths: input.IncludePaths, IncludeAdult: input.IncludeAdult,
	}, clock.NewSystem().Now().Unix())
	if err != nil {
		return apiserver.WebhookInfo{}, err
	}
	hook, err := a.p.cache.WebhookByID(ctx, saved)
	if err != nil {
		return apiserver.WebhookInfo{}, err
	}
	return a.info(ctx, hook), nil
}

// Delete implements apiserver.Webhooks.
func (a webhookAdmin) Delete(ctx context.Context, id int64) error {
	if _, err := a.p.cache.WebhookByID(ctx, id); err != nil {
		return err
	}
	if err := a.p.cache.DeleteWebhook(ctx, id); err != nil {
		return err
	}
	return library.NewWebhookPending(a.p.library).Clear(ctx, id)
}

// Test implements apiserver.Webhooks.
func (a webhookAdmin) Test(ctx context.Context, id int64) (apiserver.WebhookTest, error) {
	status, reply, err := a.p.webhookService().Test(ctx, id)
	if err != nil && status == 0 {
		if _, found := a.p.cache.WebhookByID(ctx, id); found != nil {
			return apiserver.WebhookTest{}, found
		}
		return apiserver.WebhookTest{Err: err.Error()}, nil
	}
	return apiserver.WebhookTest{OK: status >= 200 && status < 300, Status: status, Reply: reply}, nil
}

var webhookGroups = map[webhook.Event]string{
	webhook.EventGrabbed: "downloads", webhook.EventDownloadFailed: "downloads", webhook.EventRequestCreated: "requests", webhook.EventRequestApproved: "requests",
	webhook.EventHealthRaised: "health", webhook.EventHealthCleared: "health", webhook.EventTest: "test",
}

// Vocabulary implements apiserver.Webhooks.
func (webhookAdmin) Vocabulary() apiserver.WebhookVocabulary {
	var vocabulary apiserver.WebhookVocabulary
	for _, event := range webhook.Events {
		group := webhookGroups[event]
		if group == "" {
			group = "library"
		}
		vocabulary.Events = append(vocabulary.Events, apiserver.WebhookEventView{Key: string(event), Group: group})
	}
	for _, preset := range webhook.Presets() {
		vocabulary.PresetKeys = append(vocabulary.PresetKeys, preset.Key)
		vocabulary.PresetNames = append(vocabulary.PresetNames, preset.Name)
		vocabulary.PresetNotes = append(vocabulary.PresetNotes, preset.Description)
	}
	return vocabulary
}

func secretOf(value string) secrets.Secret { return secrets.Secret(value) }
