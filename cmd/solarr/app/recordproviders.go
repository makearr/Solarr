package app

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/providerdef"
)

// recordProviders are the active definition-file metadata providers by key, swapped whole at every reload.
type recordProviders struct {
	held atomic.Pointer[map[string]recordEntry]
}

// recordEntry is a built provider with what a test needs to run the same definition through a recording client.
type recordEntry struct {
	provider *specprovider.Provider
	parsed   specprovider.Definition
	client   specprovider.Doer
	values   func() map[string]string
}

func (r *recordProviders) entry(key string) (recordEntry, bool) {
	if held := r.held.Load(); held != nil {
		found, ok := (*held)[key]
		return found, ok
	}
	return recordEntry{}, false
}

func (r *recordProviders) get(key string) *specprovider.Provider {
	found, _ := r.entry(key)
	return found.provider
}

// definitionStates are the stored states of one kind by key.
func (p *process) definitionStates(kind providerdef.Kind) map[string]providerdef.State {
	states := map[string]providerdef.State{}
	stored, err := p.cache.ProviderDefinitionStates(context.Background())
	if err != nil {
		return states
	}
	for _, row := range stored {
		if row.Kind == string(kind) {
			states[row.Key] = providerdef.State{Enabled: row.Enabled, ApprovedHash: row.ApprovedHash, ApprovedHosts: row.ApprovedHosts}
		}
	}
	return states
}

// reloadRecordProviders builds a provider for every active metadata definition that is a provider itself, and offers each in
// the order pickers of the media types that can merge a generic record.
func (p *process) reloadRecordProviders() {
	states := p.definitionStates(providerdef.KindMetadata)
	built := map[string]recordEntry{}
	var offered []settings.MetadataProvider
	for _, def := range p.definitions.Catalog().List(providerdef.KindMetadata) {
		if def.Engine != providerdef.EngineHTTP || providerdef.StatusOf(def, states[def.Key]) != providerdef.StatusActive {
			continue
		}
		parsed, err := specprovider.Parse(def.Key, def.Name, def.Raw)
		if err != nil {
			p.logger.Warn("metadata definition could not be read", slog.String("key", def.Key), slog.Any("error", err))
			continue
		}
		var types []string
		for _, mediaType := range parsed.MediaTypes {
			if _, ok := media.Capability[media.RecordConsuming](p.media, media.Key(mediaType)); ok {
				types = append(types, mediaType)
			} else {
				p.logger.Warn("metadata definition names a media type that takes no definition providers", slog.String("key", def.Key), slog.String("media_type", mediaType))
			}
		}
		if len(types) == 0 {
			continue
		}
		key := def.Key
		guard := httpx.NewGuard(nil, nil, nil).OnlyHosts(parsed.Hosts)
		config := httpx.ClientConfig{}
		if parsed.Via == "flaresolverr" {
			// The solver's address is the owner's own setting; without one the site is asked direct and will say blocked.
			held, _ := p.cache.ProviderDefinitionSettings(context.Background(), string(providerdef.KindMetadata), key)
			if address := held["flaresolverr_url"]; address != "" {
				config.Proxy = p.liveProxyPolicy()
				config.Transport = p.flareSolverrTransport(guard, address, config.Proxy)
			}
		}
		client := p.providerClient(key, guard, config)
		values := func() map[string]string {
			held, _ := p.cache.ProviderDefinitionSettings(context.Background(), string(providerdef.KindMetadata), key)
			return held
		}
		built[key] = recordEntry{provider: specprovider.New(parsed, client, clock.NewSystem(), values), parsed: parsed, client: client, values: values}
		offered = append(offered, settings.MetadataProvider{
			Key: key, Label: def.Name, MediaTypes: types, Carries: slices.Clone(def.Carries), PerMinute: parsed.Pace.PerMinute,
			MaxPerMinute: max(parsed.Pace.MaxPerMinute, parsed.Pace.PerMinute), Published: def.Description, Custom: true,
			LibraryKinds: slices.Clone(parsed.LibraryKinds), Role: settings.ProviderRole(parsed.Role),
		})
	}
	p.records.held.Store(&built)
	settings.SetCustomMetadataProviders(offered)
}
