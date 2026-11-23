package apiserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// WebhookInfo is one outgoing webhook as the interface shows it: never the address or the secret.
type WebhookInfo struct {
	ID                                             int64
	Name, Preset, AddressHint, LastError           string
	Enabled, HasSecret, IncludePaths, IncludeAdult bool
	Events                                         []string
	CategoryIDs                                    []int64
	LastStatus                                     int
	LastAt, NextAttempt                            time.Time
	Pending                                        int64
}

// WebhookInput is what a person sets; an empty Address or Secret keeps the stored one.
type WebhookInput struct {
	Name, Preset, Address, Secret       string
	Enabled, IncludePaths, IncludeAdult bool
	Events                              []string
	CategoryIDs                         []int64
}

// WebhookTest is the receiver's answer to a test event.
type WebhookTest struct {
	OK     bool
	Status int
	Reply  string
	Err    string
}

// WebhookVocabulary is what a webhook may subscribe to and the presets it may use.
type WebhookVocabulary struct {
	Events                                              []WebhookEventView
	PresetKeys, PresetNames, PresetNotes, PresetSamples []string
}

// WebhookEventView names an event and the group the interface lists it under.
type WebhookEventView struct{ Key, Group string }

// WebhookWaiting is one delivery still to make.
type WebhookWaiting struct {
	ID, Attempts     int64
	Event            string
	QueuedAt, NextAt time.Time
}

// Webhooks administers the outgoing webhooks; cmd/solarr/app implements it. Invalid input answers config.Problems and an
// unknown id an error wrapping settings.ErrNotFound.
type Webhooks interface {
	List(ctx context.Context) ([]WebhookInfo, error)
	Save(ctx context.Context, id int64, input WebhookInput) (WebhookInfo, error)
	Delete(ctx context.Context, id int64) error
	Test(ctx context.Context, id int64) (WebhookTest, error)
	// Pending lists what still waits for the webhook; an unknown id answers an error wrapping settings.ErrNotFound.
	Pending(ctx context.Context, id int64) ([]WebhookWaiting, error)
	Vocabulary() WebhookVocabulary
}

func (s *Server) webhooksOf(w http.ResponseWriter) Webhooks {
	if s.webhooks == nil {
		writeError(w, s.logger, fmt.Errorf("%w: webhooks", settings.ErrNotFound))
		return nil
	}
	return s.webhooks
}

func webhookView(info WebhookInfo) gen.Webhook {
	view := gen.Webhook{
		Id: info.ID, Name: info.Name, Enabled: info.Enabled, Preset: info.Preset, AddressHint: info.AddressHint, HasSecret: info.HasSecret,
		Events: orEmpty(info.Events), CategoryIds: append([]int64{}, info.CategoryIDs...), IncludePaths: info.IncludePaths, IncludeAdult: info.IncludeAdult,
		LastError: info.LastError, Pending: info.Pending,
	}
	if !info.LastAt.IsZero() {
		status := info.LastStatus
		view.LastStatus, view.LastAt = &status, &info.LastAt
	}
	if !info.NextAttempt.IsZero() {
		view.NextAttempt = &info.NextAttempt
	}
	return view
}

func webhookInput(body gen.WebhookWrite) WebhookInput {
	return WebhookInput{
		Name: body.Name, Preset: body.Preset, Address: derefOr(body.Address, ""), Secret: derefOr(body.Secret, ""), Enabled: derefOr(body.Enabled, true),
		IncludePaths: derefOr(body.IncludePaths, false), IncludeAdult: derefOr(body.IncludeAdult, false), Events: body.Events, CategoryIDs: derefOr(body.CategoryIds, nil),
	}
}

// ListWebhooks answers every webhook.
func (s *Server) ListWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	found, err := webhooks.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.Webhook, len(found))
	for index, info := range found {
		views[index] = webhookView(info)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) saveWebhook(w http.ResponseWriter, r *http.Request, id int64, status int) {
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	var body gen.WebhookWrite
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with name, preset and events") {
		return
	}
	saved, err := webhooks.Save(r.Context(), id, webhookInput(body))
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The webhook has problems", problems.Report("the webhook", "Nothing was saved."))
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, status, webhookView(saved))
}

// AddWebhook adds a webhook.
func (s *Server) AddWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.saveWebhook(w, r, 0, http.StatusCreated)
}

// UpdateWebhook replaces a webhook.
func (s *Server) UpdateWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.saveWebhook(w, r, id, http.StatusOK)
}

// DeleteWebhook removes a webhook.
func (s *Server) DeleteWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	if err := webhooks.Delete(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestWebhook sends a test event now.
func (s *Server) TestWebhook(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	result, err := webhooks.Test(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.WebhookTestResult{Ok: result.OK, Status: result.Status, Reply: result.Reply}
	if result.Err != "" {
		view.Error = &result.Err
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// ListWebhookPending answers the deliveries a webhook still has to make.
func (s *Server) ListWebhookPending(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	waiting, err := webhooks.Pending(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.WebhookPendingDelivery, len(waiting))
	for i, one := range waiting {
		views[i] = gen.WebhookPendingDelivery{Id: one.ID, Event: one.Event, QueuedAt: one.QueuedAt, Attempts: int(one.Attempts), NextAt: one.NextAt}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// ListWebhookEvents answers the vocabulary and the presets.
func (s *Server) ListWebhookEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	webhooks := s.webhooksOf(w)
	if webhooks == nil {
		return
	}
	vocabulary := webhooks.Vocabulary()
	view := gen.WebhookVocabulary{Events: []gen.WebhookEventInfo{}, Presets: []gen.WebhookPresetInfo{}}
	for _, event := range vocabulary.Events {
		view.Events = append(view.Events, gen.WebhookEventInfo{Key: event.Key, Group: event.Group})
	}
	for index, key := range vocabulary.PresetKeys {
		view.Presets = append(view.Presets, gen.WebhookPresetInfo{Key: key, Name: vocabulary.PresetNames[index], Description: vocabulary.PresetNotes[index], Sample: vocabulary.PresetSamples[index]})
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
