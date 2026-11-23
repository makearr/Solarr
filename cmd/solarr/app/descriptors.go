package app

import (
	"slices"
	"strings"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/providerdef"
)

// descriptorKey is a provider's key as a definition's key and file name: "local:jellyfin" is "local-jellyfin".
func descriptorKey(providerKey string) string { return strings.ReplaceAll(providerKey, ":", "-") }

// compiledDescriptors are the descriptors of every provider compiled in, generated from the Go definitions so they cannot drift from them.
func compiledDescriptors() map[providerdef.Kind][]providerdef.File {
	var metadata []providerdef.File
	for _, provider := range settings.MetadataProviders {
		enabled := !slices.Contains(settings.DefaultDisabledProviders, provider.Key)
		maxPace := provider.MaxPerMinute
		if maxPace == 0 {
			maxPace = settings.MaxProviderPerMinute
		}
		metadata = append(metadata, providerdef.File{
			Kind: providerdef.KindMetadata, Key: descriptorKey(provider.Key), Name: provider.Label, Engine: providerdef.EngineBuiltin, Enabled: &enabled,
			Pace: &providerdef.Pace{PerMinute: provider.PerMinute, MaxPerMinute: maxPace}, MediaTypes: provider.MediaTypes, AlsoFor: provider.AlsoFor,
			Carries: provider.Carries, NeedsKey: provider.NeedsKey, Published: provider.Published, Role: string(provider.Role),
		})
	}
	var indexers []providerdef.File
	for _, one := range adapters.IndexerDefinitions() {
		file := providerdef.File{
			Kind: providerdef.KindIndexer, Key: one.ID, Name: one.Name, Engine: providerdef.EngineBuiltin, Description: one.Description, Impl: one.Impl,
			Privacy: string(one.Privacy), Protocol: one.Protocol(), Language: one.Language, BaseURLs: one.BaseURLs, MediaTypes: one.MediaTypes,
			Companion: one.CompanionDownloader,
		}
		if one.Credentials != (indexer.Credentials{}) {
			file.Credentials = &providerdef.Credentials{UserLabel: one.Credentials.UserLabel, SecretLabel: one.Credentials.SecretLabel, Optional: one.Credentials.Optional}
		}
		indexers = append(indexers, file)
	}
	var downloads []providerdef.File
	for _, one := range adapters.DownloadClientDefinitions() {
		downloads = append(downloads, providerdef.File{
			Kind: providerdef.KindDownload, Key: one.Impl, Name: one.Label, Engine: providerdef.EngineBuiltin, Protocol: string(one.Protocol), Impl: one.Impl,
		})
	}
	return map[providerdef.Kind][]providerdef.File{providerdef.KindMetadata: metadata, providerdef.KindIndexer: indexers, providerdef.KindDownload: downloads}
}
