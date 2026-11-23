package settings_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func TestWebhook_SealsTheAddressAndSecretAndKeepsThemWhenAnEditSendsNone(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableSecrets(cipher)
	ctx, at := t.Context(), fake.Now().Unix()
	id, err := cache.SaveWebhook(ctx, settings.Webhook{
		Name: "Chat", Preset: "discord", Enabled: true, URL: "https://hooks.example.org/token-123", Secret: "s3cret",
		Events: []string{"imported"}, CategoryIDs: []int64{3}, IncludeAdult: true,
	}, at)
	if err != nil {
		t.Fatal(err)
	}
	// An edit that sends no address and no secret keeps the sealed ones.
	if _, err := cache.SaveWebhook(ctx, settings.Webhook{ID: id, Name: "Chat 2", Preset: "slack", Enabled: true, Events: []string{"grabbed"}}, at); err != nil {
		t.Fatal(err)
	}
	got, err := cache.WebhookByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Webhook{
		ID: id, Name: "Chat 2", Preset: "slack", Enabled: true, URL: "https://hooks.example.org/token-123", Secret: "s3cret",
		Events: []string{"grabbed"}, CategoryIDs: []int64{}, CreatedAt: at,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("webhook = %+v\nwant %+v", got, want)
	}
	if _, err := cache.WebhookByID(ctx, id+1); err == nil {
		t.Fatal("an unknown id was found")
	}
}
