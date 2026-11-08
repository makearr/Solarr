package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

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
	"github.com/makearr/Solarr/internal/acquiring/indexer/spec"
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
		indexer.ImplSpec: buildSpecIndexer,
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
	taken := map[string]bool{}
	for i, definition := range definitions {
		taken[definition.ID] = true
		kinds[i] = apiserver.IndexerKind{
			ID: definition.ID, Name: definition.Name, Description: definition.Description, Impl: definition.Impl,
			Protocol: definition.Protocol(), Privacy: string(definition.Privacy), Language: definition.Language,
			BaseURLs: definition.BaseURLs, MediaTypes: definition.MediaTypes,
			UserLabel: definition.Credentials.UserLabel, SecretLabel: definition.Credentials.SecretLabel,
			SecretOptional: definition.Credentials.Optional, CompanionDownloader: definition.CompanionDownloader,
		}
	}
	return append(kinds, specKinds(taken)...)
}

// buildSpecIndexer builds a site from its spec; the secret holds the settings the owner filled in, as JSON.
func buildSpecIndexer(deps IndexerDeps, c indexer.Connection) indexer.Driver {
	found, ok := spec.Get(c.Definition)
	if !ok {
		return missingSpec{id: c.Definition}
	}
	settings := map[string]string{}
	if c.APIKey != "" {
		_ = json.Unmarshal([]byte(c.APIKey), &settings)
	}
	return spec.New(found, deps.HTTP, deps.Clock, c.BaseURL, settings)
}

// missingSpec stands in for an indexer whose spec this version no longer has.
type missingSpec struct{ id string }

func (m missingSpec) err() error {
	return fmt.Errorf("the indexer %q is not in this version's list", m.id)
}

func (m missingSpec) Capabilities(context.Context) (indexer.Capabilities, error) {
	return indexer.Capabilities{}, m.err()
}

func (m missingSpec) Search(context.Context, indexer.SearchRequest) ([]indexer.RawItem, error) {
	return nil, m.err()
}

func (m missingSpec) Fetch(context.Context, string) ([]byte, error) { return nil, m.err() }

// specKinds are the catalog entries of the specs, besides the ones Solarr has its own implementation of.
func specKinds(taken map[string]bool) []apiserver.IndexerKind {
	specs, err := spec.All()
	if err != nil {
		return nil
	}
	var kinds []apiserver.IndexerKind
	for _, one := range specs {
		if taken[one.ID] {
			continue
		}
		fields := make([]apiserver.IndexerField, 0, len(one.Settings))
		for _, setting := range one.Settings {
			switch setting.Type {
			case "text", "password", "checkbox", "select", "info":
			default:
				continue
			}
			field := apiserver.IndexerField{Name: setting.Name, Type: setting.Type, Label: setting.Label, Default: setting.Default}
			if setting.Type == "info" {
				field.Name = ""
			}
			for _, option := range setting.Options {
				field.Options = append(field.Options, apiserver.IndexerFieldOption{Value: option.Value, Label: option.Label})
			}
			fields = append(fields, field)
		}
		kinds = append(kinds, apiserver.IndexerKind{
			ID: one.ID, Name: one.Name, Description: one.Description, Impl: indexer.ImplSpec, Protocol: "torrent",
			Privacy: one.Privacy, Language: one.Language, BaseURLs: one.Links, MediaTypes: specMedia(one),
			Fields: fields, Unsupported: one.Unsupported(),
		})
	}
	return kinds
}

// specMedia are the media types a spec's categories cover.
func specMedia(one spec.Spec) []string {
	var media []string
	add := func(kind string) {
		if !slices.Contains(media, kind) {
			media = append(media, kind)
		}
	}
	for _, mapping := range one.Caps.Categories {
		switch id := mapping.NewznabID(); {
		case id == 5070:
			add(indexer.MediaAnime)
			add(indexer.MediaTV)
		case id >= 2000 && id < 3000:
			add(indexer.MediaMovies)
		case id >= 3000 && id < 4000:
			add(indexer.MediaMusic)
		case id >= 5000 && id < 6000:
			add(indexer.MediaTV)
		case id >= 6000 && id < 7000:
			add(indexer.MediaAdult)
		case id >= 7000 && id < 8000:
			add(indexer.MediaBooks)
		}
	}
	if media == nil {
		media = []string{}
	}
	return media
}
