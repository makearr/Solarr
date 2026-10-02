package adapters

import (
	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/acquiring/qbittorrent"
	"github.com/makearr/Solarr/internal/acquiring/sabnzbd"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// The download clients Solarr can drive (ADR-46); Impl is the downloader row's own value.
const (
	ImplSABnzbd     = "sabnzbd"
	ImplQBittorrent = "qbittorrent"
)

// NewDownloadClient puts one connection behind download.Client; known is false for an impl Solarr has no client for.
func NewDownloadClient(impl string, client *httpx.Client, baseURL, credentials string) (download.Client, bool) {
	switch impl {
	case ImplSABnzbd:
		return sabnzbd.Downloader{Client: sabnzbd.NewClient(client, baseURL, credentials)}, true
	case ImplQBittorrent:
		return qbittorrent.Downloader{Client: qbittorrent.NewClient(client, baseURL, credentials)}, true
	}
	return nil, false
}
