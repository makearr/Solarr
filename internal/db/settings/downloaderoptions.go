package settings

import (
	"encoding/json"
	"path"
	"strings"
)

// DownloaderOptions are a download client's Lidarr-style options, carried in settings_json.
type DownloaderOptions struct {
	// RemoveCompleted is Lidarr's "Remove Completed Downloads": once imported and verified, the
	// download leaves the client and its files are deleted (answered 2026-09-24, default on).
	RemoveCompleted *bool `json:"remove_completed,omitempty"`
	// RemotePath and LocalPath are Lidarr's Remote Path Mapping: where the client says a download
	// is, and where Solarr sees the same folder.
	RemotePath string `json:"remote_path,omitempty"`
	LocalPath  string `json:"local_path,omitempty"`
	// Category is Lidarr's client category; empty means the media type's own name ("music").
	Category string `json:"category,omitempty"`
	// MaxQueued is ADR-57's queue depth: automatic sends beyond it are held, not sent.
	MaxQueued *int `json:"max_queued,omitempty"`
	// AlwaysSendUserGrabs and AlwaysSendRSSGrabs let a user's own grabs and RSS grabs past a full
	// queue (2026-09-29, default on); off, they are held too, ahead of automatic ones.
	AlwaysSendUserGrabs *bool `json:"always_send_user_grabs,omitempty"`
	AlwaysSendRSSGrabs  *bool `json:"always_send_rss_grabs,omitempty"`
}

// DefaultMaxQueued is ADR-57's default queue depth per client.
const DefaultMaxQueued = 10

// Options reads a downloader's options; unreadable or absent means the defaults.
func (d Downloader) Options() DownloaderOptions {
	var options DownloaderOptions
	if d.SettingsJSON != "" {
		_ = json.Unmarshal([]byte(d.SettingsJSON), &options)
	}
	return options
}

// RemovesCompleted is RemoveCompleted with its default.
func (o DownloaderOptions) RemovesCompleted() bool {
	return o.RemoveCompleted == nil || *o.RemoveCompleted
}

// MaxQueuedDownloads is MaxQueued with its default.
func (o DownloaderOptions) MaxQueuedDownloads() int {
	if o.MaxQueued == nil {
		return DefaultMaxQueued
	}
	return *o.MaxQueued
}

// SendsUserGrabs is AlwaysSendUserGrabs with its default.
func (o DownloaderOptions) SendsUserGrabs() bool {
	return o.AlwaysSendUserGrabs == nil || *o.AlwaysSendUserGrabs
}

// SendsRSSGrabs is AlwaysSendRSSGrabs with its default.
func (o DownloaderOptions) SendsRSSGrabs() bool {
	return o.AlwaysSendRSSGrabs == nil || *o.AlwaysSendRSSGrabs
}

// CategoryFor is the client category a download of mediaType is sent under.
func (o DownloaderOptions) CategoryFor(mediaType string) string {
	if o.Category != "" {
		return o.Category
	}
	return mediaType
}

// LocalPathFor maps a path the client reported to where Solarr sees it; unmapped paths pass through.
func (o DownloaderOptions) LocalPathFor(reported string) string {
	if o.RemotePath == "" || o.LocalPath == "" {
		return reported
	}
	remote := strings.TrimRight(o.RemotePath, "/")
	if reported != remote && !strings.HasPrefix(reported, remote+"/") {
		return reported
	}
	return path.Join(o.LocalPath, strings.TrimPrefix(reported, remote))
}
