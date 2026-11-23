package app

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/secrets"
)

var testLibraries = []settings.Category{{ID: 3, MediaType: "tv", Name: "Shows"}, {ID: 5, MediaType: "music", Name: "Albums"}}

func TestWebhookEntryNeverCarriesAddressOrSecret(t *testing.T) {
	hook := settings.Webhook{
		ID: 9, Name: "Chat", Preset: "discord", Enabled: true, URL: secrets.Secret("https://hooks.invalid/abc"), Secret: secrets.Secret("s3cret"),
		Events: []string{"work_added"}, CategoryIDs: []int64{5, 99}, IncludePaths: true,
	}
	want := settings.WebhookEntry{Name: "Chat", Preset: "discord", Enabled: true, Events: []string{"work_added"}, Libraries: []string{"music/Albums"}, IncludePaths: true}
	got := webhookEntryOf(hook, testLibraries)
	if !slices.Equal(got.Events, want.Events) || !slices.Equal(got.Libraries, want.Libraries) || got.Name != want.Name || got.Preset != want.Preset || !got.Enabled || !got.IncludePaths || got.IncludeAdult {
		t.Fatalf("entry = %+v, want %+v", got, want)
	}
}

func TestLibraryIDsReportsMissing(t *testing.T) {
	ids, missing := libraryIDs([]string{"tv/Shows", "tv/Gone"}, testLibraries)
	if !slices.Equal(ids, []int64{3}) || !slices.Equal(missing, []string{"tv/Gone"}) {
		t.Fatalf("ids %v missing %v", ids, missing)
	}
}

func TestCheckExtrasReportsEveryFault(t *testing.T) {
	document := settings.Document{
		Webhooks: []settings.WebhookEntry{
			{Name: "ok", Preset: "discord", Libraries: []string{"tv/Shows"}},
			{Name: "", Preset: "nope", Events: []string{"bogus"}, Libraries: []string{"tv/Gone"}},
		},
		IncomingWebhooks:    []settings.IncomingWebhookEntry{{Name: "in", Actions: []string{"scan", "explode"}}},
		ProviderDefinitions: []settings.ProviderDefinitionEntry{{Kind: "metadata", Key: "x", File: "not: [valid"}, {Kind: "toaster", Key: "y"}},
	}
	problems := checkExtras(document, map[string]bool{"tv/Shows": true})
	var paths []string
	for _, problem := range problems {
		paths = append(paths, problem.Path)
	}
	want := []string{
		"webhooks[1].name", "webhooks[1].preset", "webhooks[1].events", "webhooks[1].libraries",
		"incoming_webhooks[0].actions", "provider_definitions[0].file", "provider_definitions[1].kind",
	}
	if !slices.Equal(paths, want) {
		t.Fatalf("paths = %v\nwant %v", paths, want)
	}
}

func TestCheckExtrasAcceptsACleanDocument(t *testing.T) {
	document := settings.Document{
		Webhooks:         []settings.WebhookEntry{{Name: "ok", Preset: "discord", Events: []string{"work_added"}, Libraries: []string{"tv/Shows"}}},
		IncomingWebhooks: []settings.IncomingWebhookEntry{{Name: "in", Actions: []string{"scan"}, Libraries: []string{"tv/Shows"}}},
	}
	if problems := checkExtras(document, map[string]bool{"tv/Shows": true}); len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
}
