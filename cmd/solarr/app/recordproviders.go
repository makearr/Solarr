package app

import (
	"context"
	"log/slog"
	"slices"
	"sync/atomic"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
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
		libraryKinds := slices.Clone(parsed.LibraryKinds)
		if parsed.Adult {
			types, libraryKinds = adultOnly(p.media.Descriptors(), types, libraryKinds)
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
			LibraryKinds: libraryKinds, Role: settings.ProviderRole(parsed.Role),
		})
	}
	p.records.held.Store(&built)
	settings.SetCustomMetadataProviders(offered)
}

// adultOnly narrows an `adult: true` definition to the libraries of an adult kind: the media types that have one (or are
// adult themselves) and the kinds, the definition's own list cut to the adult ones. A type with none is dropped.
func adultOnly(descriptors []media.Descriptor, types, declared []string) (kept, kinds []string) {
	for _, mediaType := range types {
		for _, descriptor := range descriptors {
			if string(descriptor.Key) != mediaType {
				continue
			}
			whole, offered := mediaType == "adult", 0
			for _, kind := range descriptor.Kinds {
				if (whole || kind.Adult) && (len(declared) == 0 || slices.Contains(declared, kind.Key)) {
					offered++
					if !slices.Contains(kinds, kind.Key) {
						kinds = append(kinds, kind.Key)
					}
				}
			}
			if offered > 0 && !slices.Contains(kept, mediaType) {
				kept = append(kept, mediaType)
			}
		}
	}
	return kept, kinds
}

// enrichmentRecords are the switched-on definitions that declare music and the enrich role, in key order.
func (p *process) enrichmentRecords() []music.RecordProvider {
	held := p.records.held.Load()
	if held == nil {
		return nil
	}
	snapshot := p.cache.Snapshot()
	var keys []string
	for key, entry := range *held {
		if entry.parsed.Role == string(settings.RoleEnrich) && slices.Contains(entry.parsed.MediaTypes, string(music.MediaType)) && snapshot.ProviderEnabled(key) {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	providers := make([]music.RecordProvider, 0, len(keys))
	for _, key := range keys {
		providers = append(providers, (*held)[key].provider)
	}
	return providers
}
