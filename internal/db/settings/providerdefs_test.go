package settings_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

func TestProviderDefinitionState_KeepsChoicesApprovalAndSealedSettingsApart(t *testing.T) {
	cache, fake := newCache(t)
	cipher, err := secrets.New(testCipherKey())
	if err != nil {
		t.Fatal(err)
	}
	cache.EnableSecrets(cipher)
	ctx, at := t.Context(), fake.Now().Unix()
	off := false
	if err := cache.SetProviderDefinitionEnabled(ctx, "metadata", "alpha", &off, at); err != nil {
		t.Fatal(err)
	}
	if err := cache.ApproveProviderDefinition(ctx, "metadata", "alpha", "h1", []string{"api.example.test"}, at); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetProviderDefinitionSettings(ctx, "metadata", "alpha", map[string]string{"token": "s3cret"}, at); err != nil {
		t.Fatal(err)
	}
	states, err := cache.ProviderDefinitionStates(ctx)
	want := []settings.ProviderDefinitionState{{Kind: "metadata", Key: "alpha", Enabled: &off, ApprovedHash: "h1", ApprovedHosts: []string{"api.example.test"}, HasSettings: true, UpdatedAt: at}}
	if err != nil || !reflect.DeepEqual(states, want) {
		t.Fatalf("states = %+v, %v; want %+v", states, err, want)
	}
	values, err := cache.ProviderDefinitionSettings(ctx, "metadata", "alpha")
	if err != nil || values["token"] != "s3cret" {
		t.Errorf("settings = %v, %v", values, err)
	}
	if err := cache.SetProviderDefinitionEnabled(ctx, "metadata", "alpha", nil, at); err != nil {
		t.Fatal(err)
	}
	if states, _ = cache.ProviderDefinitionStates(ctx); states[0].Enabled != nil || states[0].ApprovedHash != "h1" {
		t.Errorf("clearing the choice changed more: %+v", states[0])
	}
	if err := cache.ClearProviderDefinitionState(ctx, "metadata", "alpha"); err != nil {
		t.Fatal(err)
	}
	if states, _ = cache.ProviderDefinitionStates(ctx); len(states) != 0 {
		t.Errorf("states after clear = %+v", states)
	}
}
