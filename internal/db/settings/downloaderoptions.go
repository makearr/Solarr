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
}

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
