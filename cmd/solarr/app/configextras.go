package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/providerdef"
	"github.com/makearr/Solarr/internal/service/webhook"
)

// configExtras implements apiserver.ConfigExtras: webhooks and custom definition files travel in the configuration document,
// never an address, a secret or a token hash. An import adds and updates by name; it never deletes.
type configExtras struct{ p *process }

var _ apiserver.ConfigExtras = configExtras{}

// definitionKinds are the folders whose custom files are exported.
var definitionKinds = []providerdef.Kind{providerdef.KindMetadata, providerdef.KindIndexer, providerdef.KindDownload, providerdef.KindWebhook}

func libraryRef(category settings.Category) string { return category.MediaType + "/" + category.Name }

// libraryRefs names libraries by id; an id no library has any more is dropped.
func libraryRefs(ids []int64, categories []settings.Category) []string {
	var refs []string
	for _, id := range ids {
		for _, category := range categories {
			if category.ID == id {
				refs = append(refs, libraryRef(category))
			}
		}
	}
	return refs
}

// libraryIDs is the inverse of libraryRefs; refs no library answers to are returned as missing.
func libraryIDs(refs []string, categories []settings.Category) (ids []int64, missing []string) {
	for _, ref := range refs {
		found := false
		for _, category := range categories {
			if libraryRef(category) == ref {
				ids, found = append(ids, category.ID), true
				break
			}
		}
		if !found {
			missing = append(missing, ref)
		}
	}
	return ids, missing
}

func webhookEntryOf(hook settings.Webhook, categories []settings.Category) settings.WebhookEntry {
	return settings.WebhookEntry{
		Name: hook.Name, Preset: hook.Preset, Enabled: hook.Enabled, Events: slices.Clone(hook.Events),
		Libraries: libraryRefs(hook.CategoryIDs, categories), IncludePaths: hook.IncludePaths, IncludeAdult: hook.IncludeAdult,
	}
}

func incomingEntryOf(hook settings.InboundWebhook, categories []settings.Category, serverName string) settings.IncomingWebhookEntry {
	return settings.IncomingWebhookEntry{
		Name: hook.Name, Enabled: hook.Enabled, Actions: slices.Clone(hook.Actions),
		Libraries: libraryRefs(hook.CategoryIDs, categories), MediaServer: serverName,
	}
}

// Export implements apiserver.ConfigExtras.
func (e configExtras) Export(ctx context.Context, document *settings.Document) error {
	categories := e.p.cache.Snapshot().Categories()
	hooks, err := e.p.cache.Webhooks(ctx)
	if errors.Is(err, settings.ErrSecretsUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, hook := range hooks {
		document.Webhooks = append(document.Webhooks, webhookEntryOf(hook, categories))
	}
	incoming, err := e.p.cache.InboundWebhooks(ctx)
	if err != nil {
		return err
	}
	servers, err := e.p.cache.MediaServers(ctx)
	if err != nil {
		return err
	}
	for _, hook := range incoming {
		name := ""
		for _, server := range servers {
			if server.ID == hook.MediaServerID {
				name = server.Name
			}
		}
		document.IncomingWebhooks = append(document.IncomingWebhooks, incomingEntryOf(hook, categories, name))
	}
	for _, kind := range definitionKinds {
		defs, _, err := e.p.providerDefinitions().LoadCustom(kind)
		if err != nil {
			return err
		}
		for _, def := range defs {
			document.ProviderDefinitions = append(document.ProviderDefinitions, settings.ProviderDefinitionEntry{Kind: string(kind), Key: def.Key, File: string(def.Raw)})
		}
	}
	return nil
}

// checkExtras reports every fault of the three lists; known are the "media_type/name" libraries the import will leave.
func checkExtras(document settings.Document, known map[string]bool) config.Problems {
	var problems config.Problems
	bad := func(path, value, expectation string) {
		problems = append(problems, config.Problem{Path: path, Value: value, Expectation: expectation})
	}
	for index, entry := range document.Webhooks {
		path := fmt.Sprintf("webhooks[%d]", index)
		if entry.Name == "" {
			bad(path+".name", "", "a name")
		}
		if _, found := webhook.PresetByKey(entry.Preset); !found {
			bad(path+".preset", entry.Preset, "one of the presets")
		}
		for _, event := range entry.Events {
			if !webhook.Known(event) {
				bad(path+".events", event, "an event Solarr names")
			}
		}
		for _, ref := range entry.Libraries {
			if !known[ref] {
				bad(path+".libraries", ref, "a library of this configuration, as media_type/name")
			}
		}
	}
	for index, entry := range document.IncomingWebhooks {
		path := fmt.Sprintf("incoming_webhooks[%d]", index)
		if entry.Name == "" {
			bad(path+".name", "", "a name")
		}
		if len(entry.Actions) == 0 {
			bad(path+".actions", "", "at least one action")
		}
		for _, action := range entry.Actions {
			if !webhook.KnownAction(action) {
				bad(path+".actions", action, "an action Solarr names")
			}
		}
		for _, ref := range entry.Libraries {
			if !known[ref] {
				bad(path+".libraries", ref, "a library of this configuration, as media_type/name")
			}
		}
	}
	for index, entry := range document.ProviderDefinitions {
		path := fmt.Sprintf("provider_definitions[%d]", index)
		kind, ok := kindOf(entry.Kind)
		if !ok {
			bad(path+".kind", entry.Kind, "metadata, indexer, download or webhook")
			continue
		}
		def, parsed := providerdef.Parse(kind, entry.Key+".yaml", []byte(entry.File), providerdef.SourceCustom)
		if !hasErrorProblem(parsed) {
			parsed = append(parsed, checkBody(kind, def)...)
		}
		if hasErrorProblem(parsed) {
			bad(path+".file", entry.Key, "a definition file that passes validation")
		} else if def.Key != entry.Key {
			bad(path+".key", entry.Key, "the key written inside the file")
		}
	}
	return problems
}

func kindOf(name string) (providerdef.Kind, bool) {
	for _, kind := range definitionKinds {
		if string(kind) == name {
			return kind, true
		}
	}
	return "", false
}

// Check implements apiserver.ConfigExtras.
func (e configExtras) Check(_ context.Context, document settings.Document) config.Problems {
	known := map[string]bool{}
	for _, category := range e.p.cache.Snapshot().Categories() {
		known[libraryRef(category)] = true
	}
	for _, entry := range document.Categories {
		known[entry.MediaType+"/"+entry.Name] = true
	}
	return checkExtras(document, known)
}

// Apply implements apiserver.ConfigExtras. A webhook the instance does not have yet is made off and without an address: the
// address is the one thing a document never carries.
func (e configExtras) Apply(ctx context.Context, document settings.Document) error {
	now := clock.NewSystem().Now().Unix()
	categories := e.p.cache.Snapshot().Categories()
	hooks, err := e.p.cache.Webhooks(ctx)
	if err != nil {
		return err
	}
	for _, entry := range document.Webhooks {
		ids, missing := libraryIDs(entry.Libraries, categories)
		hook := settings.Webhook{
			Name: entry.Name, Preset: entry.Preset, Enabled: entry.Enabled && len(missing) == 0, Events: slices.Clone(entry.Events),
			CategoryIDs: ids, IncludePaths: entry.IncludePaths, IncludeAdult: entry.IncludeAdult,
		}
		if index := slices.IndexFunc(hooks, func(held settings.Webhook) bool { return held.Name == entry.Name }); index >= 0 {
			hook.ID = hooks[index].ID
		} else {
			hook.Enabled = false
		}
		if _, err := e.p.cache.SaveWebhook(ctx, hook, now); err != nil {
			return err
		}
	}
	return e.applyIncoming(ctx, document, categories, now)
}

func (e configExtras) applyIncoming(ctx context.Context, document settings.Document, categories []settings.Category, now int64) error {
	held, err := e.p.cache.InboundWebhooks(ctx)
	if err != nil {
		return err
	}
	servers, err := e.p.cache.MediaServers(ctx)
	if err != nil {
		return err
	}
	for _, entry := range document.IncomingWebhooks {
		ids, missing := libraryIDs(entry.Libraries, categories)
		hook := settings.InboundWebhook{Name: entry.Name, Enabled: entry.Enabled && len(missing) == 0, Actions: slices.Clone(entry.Actions), CategoryIDs: ids}
		if entry.MediaServer != "" {
			index := slices.IndexFunc(servers, func(server settings.MediaServer) bool { return strings.EqualFold(server.Name, entry.MediaServer) })
			if index < 0 {
				hook.Enabled = false
			} else {
				hook.MediaServerID = servers[index].ID
			}
		}
		if index := slices.IndexFunc(held, func(one settings.InboundWebhook) bool { return one.Name == entry.Name }); index >= 0 {
			hook.ID = held[index].ID
		} else {
			// A new token nobody saw: the person rotates it to get one, so the hook starts off.
			_, hash, err := webhook.NewRandomToken()
			if err != nil {
				return err
			}
			hook.TokenHash, hook.Enabled = hash, false
		}
		if _, err := e.p.cache.SaveInboundWebhook(ctx, hook, now); err != nil {
			return err
		}
	}
	for _, entry := range document.ProviderDefinitions {
		kind, _ := kindOf(entry.Kind)
		if err := e.p.providerDefinitions().WriteCustom(kind, entry.Key, []byte(entry.File)); err != nil {
			return err
		}
	}
	if len(document.ProviderDefinitions) > 0 {
		e.p.reloadProviderDefinitions()
	}
	return nil
}
