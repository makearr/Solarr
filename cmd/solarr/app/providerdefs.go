package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/internal/acquiring/indexer/spec"
	"github.com/makearr/Solarr/internal/providerdef"
)

// providerDefinitionsReadme is written to the providers folder at every start.
const providerDefinitionsReadme = `# Provider definitions

These YAML files describe providers: what Solarr asks, never code it runs.

- metadata, indexers, download and webhooks each hold builtin/ and custom/. builtin/ is rewritten at every start; an edit there is overwritten. It is a readable copy of what ships.
- custom/ is yours. A file with the same key as a built-in one replaces it. Solarr never changes this folder except
  through Settings > Providers.
- A custom file that is a provider itself starts inactive and must be approved by an administrator, who sees its hosts.
- Removing or reverting a file moves it to custom/.removed/.

Kinds: metadata, indexer, download, webhook. Every file starts with the same envelope: schema, kind, key, name, engine.
Built-in indexer data is converted from github.com/Prowlarr/Indexers (GPL-3.0); see ATTRIBUTION.md.
`

// providerDefinitions is the definition folder under the config folder.
func (p *process) providerDefinitions() providerdef.Store {
	return providerdef.Store{FS: p.fsys, Root: filepath.Join(p.config.Paths.ConfigDir, "providers")}
}

// prepareProviderDefinitions creates the folders and mirrors the built-in definitions; a folder that cannot be written is a
// warning, the embedded data serves either way.
func (p *process) prepareProviderDefinitions(ctx context.Context) {
	if err := p.providerDefinitions().Prepare(ctx, []byte(providerDefinitionsReadme), p.builtinDefinitions()); err != nil {
		p.logger.Warn("provider definitions folder could not be prepared; the built-in data is used", slog.Any("error", err))
	}
}

// builtinDefinitions are the files the built-in data mirrors into the builtin folders, by kind and key.
func (p *process) builtinDefinitions() map[providerdef.Kind]map[string][]byte {
	indexers := map[string][]byte{}
	embedded, err := spec.Embedded()
	if err != nil {
		p.logger.Warn("built-in indexer specs could not be read", slog.Any("error", err))
		return nil
	}
	for _, one := range embedded.All() {
		data, err := one.YAML()
		if err != nil {
			p.logger.Warn("built-in indexer spec could not be written out", slog.String("id", one.ID), slog.Any("error", err))
			continue
		}
		indexers[one.ID] = data
	}
	return map[providerdef.Kind]map[string][]byte{providerdef.KindIndexer: indexers}
}

// reloadProviderDefinitions reads every custom folder and swaps in a new catalog; a bad file is logged and the rest still load.
func (p *process) reloadProviderDefinitions() {
	store := p.providerDefinitions()
	var custom []providerdef.Definition
	for _, kind := range providerdef.Kinds {
		defs, reports, err := store.LoadCustom(kind)
		if err != nil {
			p.logger.Warn("provider definitions could not be read", slog.String("kind", string(kind)), slog.Any("error", err))
			continue
		}
		for _, report := range reports {
			for _, problem := range report.Problems {
				p.logger.Warn("provider definition problem", slog.String("kind", string(kind)), slog.String("file", report.File), slog.String("problem", problem.String()))
			}
		}
		custom = append(custom, defs...)
	}
	p.definitions.Swap(providerdef.NewCatalog(nil, custom))
	p.reloadSpecs(custom)
}

// reloadSpecs puts the approved custom indexer specs over the embedded set; one that needs approval or fails to decode is
// left out and logged.
func (p *process) reloadSpecs(custom []providerdef.Definition) {
	embedded, err := spec.Embedded()
	if err != nil {
		return
	}
	states := map[string]providerdef.State{}
	if stored, err := p.cache.ProviderDefinitionStates(context.Background()); err == nil {
		for _, row := range stored {
			if row.Kind == string(providerdef.KindIndexer) {
				states[row.Key] = providerdef.State{Enabled: row.Enabled, ApprovedHash: row.ApprovedHash, ApprovedHosts: row.ApprovedHosts}
			}
		}
	}
	p.specs.Swap(embedded.With(approvedSpecs(custom, states, func(key string, err error) {
		p.logger.Warn("custom indexer definition could not be read", slog.String("key", key), slog.Any("error", err))
	})))
}

// approvedSpecs are the custom indexer specs that are active under their stored state; a spec still needing approval is left out.
func approvedSpecs(custom []providerdef.Definition, states map[string]providerdef.State, problem func(key string, err error)) []spec.Spec {
	var active []spec.Spec
	for _, def := range custom {
		if def.Kind != providerdef.KindIndexer || def.Engine != providerdef.EngineSpec || providerdef.StatusOf(def, states[def.Key]) != providerdef.StatusActive {
			continue
		}
		found, err := spec.FromYAML(def.Raw)
		if err != nil {
			problem(def.Key, err)
			continue
		}
		active = append(active, found)
	}
	return active
}
