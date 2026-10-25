package settings

import (
	"encoding/json"
	"path"
	"strings"
	"time"
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
	// SeedRatio and SeedMinutes keep an imported torrent in the client until it has shared that much, or for
	// that long, whichever comes first; both unset removes it at once (2026-10-02, off by default).
	SeedRatio   *float64 `json:"seed_ratio,omitempty"`
	SeedMinutes *int     `json:"seed_minutes,omitempty"`
	// StallCleanup removes and blocklists Solarr's own downloads that stop making progress (2026-10-15, off by default).
	StallCleanup *StallCleanup `json:"stall_cleanup,omitempty"`
	// URLBase is the path of a client's API below its address (Transmission's /transmission/, rTorrent's RPC2).
	URLBase string `json:"url_base,omitempty"`
	// DropFolder and WatchFolder are a blackhole's: where Solarr writes what it sends, and where finished downloads
	// appear, both as Solarr sees them.
	DropFolder  string `json:"drop_folder,omitempty"`
	WatchFolder string `json:"watch_folder,omitempty"`
	// SaveMagnetFiles lets a torrent blackhole write a magnet link to a file rather than refuse it.
	SaveMagnetFiles bool `json:"save_magnet_files,omitempty"`
}

// StallCleanup is one client's stalled-download cleanup; an absent number takes its default.
type StallCleanup struct {
	Enabled bool `json:"enabled"`
	// Strikes is how many checks in a row may find no progress before the download is removed.
	Strikes *int `json:"strikes,omitempty"`
	// CheckMinutes is how often a download's progress is compared.
	CheckMinutes *int `json:"check_minutes,omitempty"`
	// NoProgressHours removes a download that has not moved for this long, whatever the strikes.
	NoProgressHours *int `json:"no_progress_hours,omitempty"`
	// IgnoreAboveGB leaves bigger downloads alone; 0 is no limit.
	IgnoreAboveGB *int `json:"ignore_above_gb,omitempty"`
}

// The stall cleanup's defaults (fillarr's Swaparr-style ones, user 2026-10-15).
const (
	DefaultStallStrikes         = 5
	DefaultStallCheckMinutes    = 15
	DefaultStallNoProgressHours = 2
	DefaultStallIgnoreAboveGB   = 50
)

// StallRules is a client's stall cleanup with its defaults filled in.
type StallRules struct {
	Enabled          bool
	Strikes          int
	Check            time.Duration
	NoProgress       time.Duration
	IgnoreAboveBytes int64
}

// StallRules resolves the stall cleanup; absent is off.
func (o DownloaderOptions) StallRules() StallRules {
	cleanup := StallCleanup{}
	if o.StallCleanup != nil {
		cleanup = *o.StallCleanup
	}
	value := func(set *int, fallback int) int {
		if set == nil {
			return fallback
		}
		return *set
	}
	return StallRules{
		Enabled:          cleanup.Enabled,
		Strikes:          value(cleanup.Strikes, DefaultStallStrikes),
		Check:            time.Duration(value(cleanup.CheckMinutes, DefaultStallCheckMinutes)) * time.Minute,
		NoProgress:       time.Duration(value(cleanup.NoProgressHours, DefaultStallNoProgressHours)) * time.Hour,
		IgnoreAboveBytes: int64(value(cleanup.IgnoreAboveGB, DefaultStallIgnoreAboveGB)) << 30,
	}
}

// SeedGoal is the ratio and minutes an imported torrent seeds for; 0 is no limit of that kind, and both 0 means none.
func (o DownloaderOptions) SeedGoal() (ratio float64, minutes int) {
	if o.SeedRatio != nil {
		ratio = *o.SeedRatio
	}
	if o.SeedMinutes != nil {
		minutes = *o.SeedMinutes
	}
	return ratio, minutes
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

// CategoryFor is the client category a download of mediaType is sent under; the default is Solarr's own, since the
// *Arrs' defaults ("music", "tv", "movies") would hand Solarr's downloads to an *Arr running beside it.
func (o DownloaderOptions) CategoryFor(mediaType string) string {
	if o.Category != "" {
		return o.Category
	}
	return "solarr-" + mediaType
}

// LocalPathFor maps a path the client reported to where Solarr sees it; unmapped paths pass through.
func (o DownloaderOptions) LocalPathFor(reported string) string {
	if o.RemotePath == "" || o.LocalPath == "" {
		return reported
	}
	remote := path.Clean(o.RemotePath)
	cleanReported := path.Clean(reported)
	if cleanReported != remote && !strings.HasPrefix(cleanReported, strings.TrimSuffix(remote, "/")+"/") {
		return reported
	}
	return path.Join(o.LocalPath, strings.TrimPrefix(cleanReported, remote))
}
