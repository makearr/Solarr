package adapters

import (
	"github.com/makearr/Solarr/internal/acquiring/aria2"
	"github.com/makearr/Solarr/internal/acquiring/blackhole"
	"github.com/makearr/Solarr/internal/acquiring/deluge"
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/flood"
	"github.com/makearr/Solarr/internal/acquiring/nzbget"
	"github.com/makearr/Solarr/internal/acquiring/qbittorrent"
	"github.com/makearr/Solarr/internal/acquiring/rtorrent"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/acquiring/slskd"
	"github.com/makearr/Solarr/internal/acquiring/transmission"
	"github.com/makearr/Solarr/internal/acquiring/utorrent"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Two of the download clients Solarr can drive (ADR-46) by the downloader row's own impl value; the rest are named only
// by their definitions.
const (
	ImplSABnzbd     = "sabnzbd"
	ImplQBittorrent = "qbittorrent"
)

// DownloadClientDeps are what building a client needs besides its own row: HTTP for a client with an API, the
// filesystem and clock for a blackhole.
type DownloadClientDeps struct {
	HTTP  *httpx.Client
	FS    fsx.FS
	Clock clock.Clock
}

// downloadClientKind is one kind the user may pick, and how a connection of it is built on its base.
type downloadClientKind struct {
	definition download.Definition
	build      func(deps DownloadClientDeps, connection download.Connection) download.Driver
}

// downloadClientKinds are every kind Solarr offers, usenet first, in the order the settings screen lists them.
func downloadClientKinds() []downloadClientKind {
	folders := func(deps DownloadClientDeps, connection download.Connection) blackhole.Folders {
		return blackhole.Folders{FS: deps.FS, Clock: deps.Clock, Drop: connection.DropFolder, Watch: connection.WatchFolder}
	}
	return []downloadClientKind{
		{sabnzbd.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewUsenet(sabnzbd.Downloader{Client: sabnzbd.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{nzbget.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewUsenet(nzbget.Downloader{Client: nzbget.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{blackhole.UsenetDefinition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewUsenet(blackhole.Usenet{Folders: folders(deps, connection)})
		}},
		{qbittorrent.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewTorrent(qbittorrent.Downloader{Client: qbittorrent.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{transmission.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			client := transmission.NewClient(deps.HTTP, connection.BaseURL, connection.URLBase, connection.Credentials)
			return download.NewTorrent(transmission.Downloader{Client: client})
		}},
		{deluge.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewTorrent(deluge.Downloader{Client: deluge.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{rtorrent.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			client := rtorrent.NewClient(deps.HTTP, connection.BaseURL, connection.URLBase, connection.Credentials)
			return download.NewTorrent(rtorrent.Downloader{Client: client})
		}},
		{utorrent.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewTorrent(utorrent.Downloader{Client: utorrent.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{transmission.VuzeDefinition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			client := transmission.NewClient(deps.HTTP, connection.BaseURL, connection.URLBase, connection.Credentials)
			return download.NewTorrent(transmission.Downloader{Client: client, Vuze: true})
		}},
		{flood.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewTorrent(flood.Downloader{Client: flood.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
		{aria2.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			client := aria2.NewClient(deps.HTTP, connection.BaseURL, connection.URLBase, connection.Credentials)
			return download.NewTorrent(aria2.Downloader{Client: client})
		}},
		{blackhole.TorrentDefinition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewTorrent(blackhole.Torrent{Folders: folders(deps, connection), SaveMagnetFiles: connection.SaveMagnetFiles})
		}},
		{slskd.Definition(), func(deps DownloadClientDeps, connection download.Connection) download.Driver {
			return download.NewDirect(slskd.Downloader{Client: slskd.NewClient(deps.HTTP, connection.BaseURL, connection.Credentials)})
		}},
	}
}

// DownloadClientDefinitions answers every kind Solarr offers, in the settings screen's order.
func DownloadClientDefinitions() []download.Definition {
	kinds := downloadClientKinds()
	definitions := make([]download.Definition, len(kinds))
	for i, kind := range kinds {
		definitions[i] = kind.definition
	}
	return definitions
}

// DownloadClientDefinition answers one kind by its impl; known is false for one Solarr has no client for.
func DownloadClientDefinition(impl string) (download.Definition, bool) {
	for _, kind := range downloadClientKinds() {
		if kind.definition.Impl == impl {
			return kind.definition, true
		}
	}
	return download.Definition{}, false
}

// NewDownloadClient puts one connection behind its kind's base; known is false for an impl Solarr has no client for.
func NewDownloadClient(impl string, deps DownloadClientDeps, connection download.Connection) (download.Driver, bool) {
	for _, kind := range downloadClientKinds() {
		if kind.definition.Impl == impl {
			return kind.build(deps, connection), true
		}
	}
	return nil, false
}

// DownloadConnection is what a client is built from: its row's address, its credentials and its own options.
func DownloadConnection(baseURL, credentials string, options settings.DownloaderOptions) download.Connection {
	return download.Connection{
		BaseURL: baseURL, Credentials: credentials, URLBase: options.URLBase,
		DropFolder: options.DropFolder, WatchFolder: options.WatchFolder, SaveMagnetFiles: options.SaveMagnetFiles,
	}
}

// DownloaderKinds is apiserver.DownloaderKinds over the definitions: what the add dialog offers.
type DownloaderKinds struct{}

// DownloaderKinds answers every kind, in the settings screen's order.
func (DownloaderKinds) DownloaderKinds() []apiserver.DownloaderKind {
	definitions := DownloadClientDefinitions()
	kinds := make([]apiserver.DownloaderKind, len(definitions))
	for i, definition := range definitions {
		fields := make([]apiserver.DownloaderField, len(definition.Fields))
		for j, field := range definition.Fields {
			fields[j] = apiserver.DownloaderField{
				Key: string(field.Key), Kind: string(field.Kind), Label: field.Label, Help: field.Help,
				Placeholder: field.Placeholder, Required: field.Required,
			}
		}
		kinds[i] = apiserver.DownloaderKind{
			Impl: definition.Impl, Label: definition.Label, Protocol: string(definition.Protocol),
			AddressPlaceholder: definition.AddressPlaceholder, SecretLabel: definition.SecretLabel, UserName: definition.UserName,
			KeepsCompleted: definition.KeepsCompleted, Categories: definition.Categories, Fields: fields,
		}
	}
	return kinds
}
