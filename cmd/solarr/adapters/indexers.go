package adapters

import (
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/indexer/beyondhd"
	"github.com/makearr/Solarr/internal/acquiring/indexer/broadcasthenet"
	"github.com/makearr/Solarr/internal/acquiring/indexer/filelist"
	"github.com/makearr/Solarr/internal/acquiring/indexer/gazelle"
	"github.com/makearr/Solarr/internal/acquiring/indexer/hdbits"
	"github.com/makearr/Solarr/internal/acquiring/indexer/internetarchive"
	"github.com/makearr/Solarr/internal/acquiring/indexer/knaben"
	"github.com/makearr/Solarr/internal/acquiring/indexer/nyaa"
	"github.com/makearr/Solarr/internal/acquiring/indexer/passthepopcorn"
	"github.com/makearr/Solarr/internal/acquiring/indexer/soulseek"
	"github.com/makearr/Solarr/internal/acquiring/indexer/subsplease"
	"github.com/makearr/Solarr/internal/acquiring/indexer/thepiratebay"
	"github.com/makearr/Solarr/internal/acquiring/indexer/yts"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// IndexerDeps are what building an indexer needs besides its own row.
type IndexerDeps struct {
	HTTP  *httpx.Client
	Clock clock.Clock
}

type indexerBuild func(deps IndexerDeps, connection indexer.Connection) indexer.Driver

// indexerImplementations build each implementation; several catalog entries share one (every Newznab preset).
func indexerImplementations() map[string]indexerBuild {
	return map[string]indexerBuild{
		indexer.ImplNewznab: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return indexer.NewClient(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplTorznab: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return indexer.NewClient(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplThePirateBay: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return thepiratebay.New(deps.HTTP, c.BaseURL)
		},
		indexer.ImplNyaa: func(deps IndexerDeps, c indexer.Connection) indexer.Driver { return nyaa.New(deps.HTTP, c.BaseURL) },
		indexer.ImplYTS:  func(deps IndexerDeps, c indexer.Connection) indexer.Driver { return yts.New(deps.HTTP, c.BaseURL) },
		indexer.ImplKnaben: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return knaben.New(deps.HTTP, c.BaseURL)
		},
		indexer.ImplInternetArchive: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return internetarchive.New(deps.HTTP, c.BaseURL)
		},
		indexer.ImplSubsPlease: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return subsplease.New(deps.HTTP, c.BaseURL)
		},
		indexer.ImplRedacted: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return gazelle.NewRedacted(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplOrpheus: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return gazelle.NewOrpheus(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplFileList: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return filelist.New(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplHDBits: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return hdbits.New(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplPassThePopcorn: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return passthepopcorn.New(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplBeyondHD: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return beyondhd.New(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplBroadcastheNet: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return broadcasthenet.New(deps.HTTP, c.BaseURL, c.APIKey)
		},
		indexer.ImplSoulseek: func(deps IndexerDeps, c indexer.Connection) indexer.Driver {
			return soulseek.New(deps.HTTP, deps.Clock, c.BaseURL, c.APIKey)
		},
	}
}

// IndexerDefinitions answers the whole catalog, usenet presets first, as the add list shows it.
func IndexerDefinitions() []indexer.Definition {
	definitions := indexer.NewznabDefinitions()
	return append(definitions,
		thepiratebay.Definition(), nyaa.Definition(), yts.Definition(), knaben.Definition(), internetarchive.Definition(),
		subsplease.Definition(), gazelle.RedactedDefinition(), gazelle.OrpheusDefinition(), filelist.Definition(),
		hdbits.Definition(), passthepopcorn.Definition(), beyondhd.Definition(), broadcasthenet.Definition(),
		soulseek.Definition(),
	)
}

// IndexerDefinition answers one catalog entry by its id.
func IndexerDefinition(id string) (indexer.Definition, bool) {
	for _, definition := range IndexerDefinitions() {
		if definition.ID == id {
			return definition, true
		}
	}
	return indexer.Definition{}, false
}

// NewIndexer builds one connection; known is false for an implementation Solarr has none of.
func NewIndexer(impl string, deps IndexerDeps, connection indexer.Connection) (indexer.Driver, bool) {
	build, known := indexerImplementations()[impl]
	if !known {
		return nil, false
	}
	return build(deps, connection), true
}

// IndexerKinds is apiserver.IndexerKinds over the catalog: what the add list offers.
type IndexerKinds struct{}

// IndexerKinds answers the catalog in the add list's order.
func (IndexerKinds) IndexerKinds() []apiserver.IndexerKind {
	definitions := IndexerDefinitions()
	kinds := make([]apiserver.IndexerKind, len(definitions))
	for i, definition := range definitions {
		kinds[i] = apiserver.IndexerKind{
			ID: definition.ID, Name: definition.Name, Description: definition.Description, Impl: definition.Impl,
			Protocol: definition.Protocol(), Privacy: string(definition.Privacy), Language: definition.Language,
			BaseURLs: definition.BaseURLs, MediaTypes: definition.MediaTypes,
			UserLabel: definition.Credentials.UserLabel, SecretLabel: definition.Credentials.SecretLabel,
			SecretOptional: definition.Credentials.Optional, CompanionDownloader: definition.CompanionDownloader,
		}
	}
	return kinds
}
