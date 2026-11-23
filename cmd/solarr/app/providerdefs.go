package app

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/makearr/Solarr/internal/providerdef"
)

// providerDefinitionsReadme is written to the providers folder at every start.
const providerDefinitionsReadme = `# Provider definitions

These YAML files describe providers: what Solarr asks, never code it runs.

- <kind>/builtin/ is rewritten at every start; an edit there is overwritten. It is a readable copy of what ships.
- <kind>/custom/ is yours. A file with the same key as a built-in one replaces it. Solarr never changes this folder except
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
	if err := p.providerDefinitions().Prepare(ctx, []byte(providerDefinitionsReadme), nil); err != nil {
		p.logger.Warn("provider definitions folder could not be prepared; the built-in data is used", slog.Any("error", err))
	}
}
