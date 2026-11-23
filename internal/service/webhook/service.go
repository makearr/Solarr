package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Backoff is the wait after the n-th failed attempt; the row is dropped after the last.
var Backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

const (
	// maxAttempts is the eighth failure, which drops the row.
	maxAttempts = 8
	runBatch    = 200
	replyBytes  = 500
	timeout     = 10 * time.Second
)

// Hooks is the store of webhooks.
type Hooks interface {
	Webhooks(ctx context.Context) ([]settings.Webhook, error)
	WebhookByID(ctx context.Context, id int64) (settings.Webhook, error)
	SetWebhookResult(ctx context.Context, id int64, status int, message string, at int64) error
	SetWebhookEnabled(ctx context.Context, id int64, enabled bool) error
}

// Outbox is the queue of deliveries.
type Outbox interface {
	Add(ctx context.Context, rows []library.WebhookDelivery) error
	Due(ctx context.Context, now, limit int64) ([]library.WebhookDelivery, error)
	Done(ctx context.Context, id int64) error
	Retry(ctx context.Context, id, nextAt int64) error
}

// Doer sends one request; the caller's client is guarded for the address and follows no redirect to another host.
type Doer interface {
	Do(ctx context.Context, webhook settings.Webhook, request *http.Request) (*http.Response, error)
}

// Service records events into the outbox and delivers them.
type Service struct {
	Hooks    Hooks
	Outbox   Outbox
	Doer     Doer
	Clock    clock.Clock
	Instance Instance
	Logger   *slog.Logger
}

// subscribed says a webhook takes the occurrence: on, subscribed, its library, and the adult gate.
func subscribed(hook settings.Webhook, occurrence Occurrence) bool {
	if !hook.Enabled || !slices.Contains(hook.Events, string(occurrence.Event)) {
		return false
	}
	// An instance-wide event (health) belongs to no library, so the library filter does not apply.
	if occurrence.Library.ID != 0 && len(hook.CategoryIDs) > 0 && !slices.Contains(hook.CategoryIDs, occurrence.Library.ID) {
		return false
	}
	return !occurrence.Adult || hook.IncludeAdult
}

// Record queues the occurrence for every webhook that takes it.
func (s Service) Record(ctx context.Context, occurrence Occurrence) error {
	hooks, err := s.Hooks.Webhooks(ctx)
	if err != nil {
		return err
	}
	now := s.Clock.Now().Unix()
	var rows []library.WebhookDelivery
	for _, hook := range hooks {
		if !subscribed(hook, occurrence) {
			continue
		}
		encoded, err := json.Marshal(payloadOf(occurrence, s.Instance, now, hook.IncludePaths))
		if err != nil {
			return err
		}
		rows = append(rows, library.WebhookDelivery{WebhookID: hook.ID, Event: string(occurrence.Event), Payload: string(encoded), QueuedAt: now, NextAt: now})
	}
	if len(rows) == 0 {
		return nil
	}
	return s.Outbox.Add(ctx, rows)
}

// Signature is the X-Solarr-Signature value of a body under a secret.
func Signature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// send makes one request for a payload and answers the status and the start of the reply.
func (s Service) send(ctx context.Context, hook settings.Webhook, stored string, delivery int64) (int, string, error) {
	preset, ok := PresetByKey(hook.Preset)
	if !ok {
		return 0, "", fmt.Errorf("webhook: no preset %q", hook.Preset)
	}
	body, err := withDelivery(stored, delivery)
	if err != nil {
		return 0, "", err
	}
	var payload Payload
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, "", err
	}
	rendered, err := preset.Render(payload)
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, firstOf(preset.Method, http.MethodPost), hook.URL.Reveal(), bytes.NewReader(rendered))
	if err != nil {
		return 0, "", errors.New("the address is not usable")
	}
	request.Header.Set("Content-Type", firstOf(preset.ContentType, "application/json"))
	for name, value := range preset.Headers {
		request.Header.Set(name, value)
	}
	request.Header.Set("X-Solarr-Event", string(payload.Event))
	request.Header.Set("X-Solarr-Delivery", strconv.FormatInt(delivery, 10))
	if hook.Secret != "" {
		request.Header.Set("X-Solarr-Signature", Signature(hook.Secret.Reveal(), rendered))
	}
	response, err := s.Doer.Do(ctx, hook, request)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = response.Body.Close() }()
	reply, _ := io.ReadAll(io.LimitReader(response.Body, replyBytes))
	return response.StatusCode, string(reply), nil
}

func firstOf(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

// Deliver sends the due rows, oldest first: 2xx removes the row, a failure backs off, the eighth drops it, a 410 switches the
// webhook off. A row of a webhook that is gone or off is dropped.
func (s Service) Deliver(ctx context.Context) error {
	now := s.Clock.Now()
	due, err := s.Outbox.Due(ctx, now.Unix(), runBatch)
	if err != nil {
		return err
	}
	hooks, err := s.Hooks.Webhooks(ctx)
	if err != nil {
		return err
	}
	byID := map[int64]settings.Webhook{}
	for _, hook := range hooks {
		byID[hook.ID] = hook
	}
	for _, row := range due {
		hook, found := byID[row.WebhookID]
		if !found || !hook.Enabled {
			if err := s.Outbox.Done(ctx, row.ID); err != nil {
				return err
			}
			continue
		}
		status, reply, sendErr := s.send(ctx, hook, row.Payload, row.ID)
		switch {
		case sendErr == nil && status >= 200 && status < 300:
			err = errors.Join(s.Outbox.Done(ctx, row.ID), s.Hooks.SetWebhookResult(ctx, hook.ID, status, "", now.Unix()))
		case sendErr == nil && status == http.StatusGone:
			err = errors.Join(s.Outbox.Done(ctx, row.ID), s.Hooks.SetWebhookEnabled(ctx, hook.ID, false), s.Hooks.SetWebhookResult(ctx, hook.ID, status, "the receiver answered 410 Gone; the webhook was switched off", now.Unix()))
		default:
			message := failure(sendErr, status, reply)
			err = s.Hooks.SetWebhookResult(ctx, hook.ID, status, message, now.Unix())
			if row.Attempts+1 >= maxAttempts {
				err = errors.Join(err, s.Outbox.Done(ctx, row.ID))
			} else {
				err = errors.Join(err, s.Outbox.Retry(ctx, row.ID, now.Add(Backoff[min(int(row.Attempts), len(Backoff)-1)]).Unix()))
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func failure(err error, status int, reply string) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("the receiver answered %d: %s", status, reply)
}

// Test sends a test event to one webhook now and answers the status and the start of the reply.
func (s Service) Test(ctx context.Context, id int64) (int, string, error) {
	hook, err := s.Hooks.WebhookByID(ctx, id)
	if err != nil {
		return 0, "", err
	}
	now := s.Clock.Now().Unix()
	stored, err := json.Marshal(payloadOf(Occurrence{Event: EventTest, Message: "A test from Solarr."}, s.Instance, now, false))
	if err != nil {
		return 0, "", err
	}
	status, reply, err := s.send(ctx, hook, string(stored), 0)
	message := ""
	if err != nil || status < 200 || status >= 300 {
		message = failure(err, status, reply)
	}
	if recordErr := s.Hooks.SetWebhookResult(ctx, id, status, message, now); recordErr != nil {
		return status, reply, recordErr
	}
	return status, reply, err
}
