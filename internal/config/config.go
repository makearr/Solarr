// Package config loads and validates config.yml, the bootstrap file (ADR-21, ADR-44).
package config

import "time"

// CurrentVersion is the config schema version this binary writes and
// understands. It is not the application version (§14.2).
const CurrentVersion = 1

// Config is config.yml. Every field has a default that works untouched.
type Config struct {
	Version int `yaml:"version"`

	Server    Server    `yaml:"server"`
	Paths     Paths     `yaml:"paths"`
	Security  Security  `yaml:"security"`
	Database  Database  `yaml:"database"`
	Backup    Backup    `yaml:"backup"`
	Retention Retention `yaml:"retention"`
	Storage   Storage   `yaml:"storage"`
	Workers   Workers   `yaml:"workers"`
	Logging   Logging   `yaml:"logging"`
	Network   Network   `yaml:"network"`

	// Mode is the dry-run level, enforced at the job executor (ADR-20).
	Mode Mode `yaml:"mode"`
}

// DefaultPublicIPEndpoint answers with a bare address and nothing else, which
// keeps the parse trivial and the response too small to carry a surprise.
const DefaultPublicIPEndpoint = "https://api.ipify.org"

// Network is outbound behaviour that is not a provider's own configuration.
type Network struct {
	PublicIPCheck PublicIPCheck `yaml:"public_ip_check"`
}

// PublicIPCheck asks a third party what address this host connects from. It is
// the only unprompted outbound call MediaNexus makes, so it is switchable off
// and names the service it asks.
type PublicIPCheck struct {
	Enabled  bool     `yaml:"enabled"`
	Endpoint string   `yaml:"endpoint"`
	Interval Duration `yaml:"interval"`
}

// Server is the HTTP listener.
type Server struct {
	Bind    string `yaml:"bind"`
	Port    int    `yaml:"port"`
	BaseURL string `yaml:"base_url"`
}

// Paths are the on-disk locations. Defaults nest under ConfigDir so a single
// volume mount is enough (§14).
type Paths struct {
	ConfigDir string `yaml:"config_dir"`
	DataDir   string `yaml:"data_dir"`
	DBDir     string `yaml:"db_dir"`
	CacheDir  string `yaml:"cache_dir"`
	BackupDir string `yaml:"backup_dir"`
}

// Security holds the encryption key and how users are authenticated (ADR-14).
type Security struct {
	// EncryptionKey is generated on first run if absent. It never appears in
	// validation output or logs (Invariant 61).
	EncryptionKey      string   `yaml:"encryption_key"`
	AuthMode           AuthMode `yaml:"auth_mode"`
	TrustedProxyHeader string   `yaml:"trusted_proxy_header"`
	TrustedProxyCIDRs  []string `yaml:"trusted_proxy_cidrs"`
}

// Database tunes SQLite (ADR-2, ADR-3).
type Database struct {
	Driver      Driver   `yaml:"driver"`
	BusyTimeout Duration `yaml:"busy_timeout"`
	// PageSize is applied at creation only and cannot change later (ADR-22).
	PageSize    int      `yaml:"page_size"`
	CacheSizeKB int      `yaml:"cache_size_kb"`
	WriteWarn   Duration `yaml:"write_warn"`
	WriteHard   Duration `yaml:"write_hard"`
}

// Backup is the automatic backup policy (ADR-15).
type Backup struct {
	Schedule string `yaml:"schedule"`
	// SettingsOnChange snapshots settings.db on every write; it is tiny and
	// precious (ADR-25).
	SettingsOnChange bool `yaml:"settings_on_change"`
	Keep             int  `yaml:"keep"`
	KeepSettings     int  `yaml:"keep_settings"`
	BeforeMigration  bool `yaml:"before_migration"`
}

// Retention is how long decision detail is kept before rollup (Part VI Q5).
type Retention struct {
	DecisionsFull Duration `yaml:"decisions_full"`
	Candidates    Duration `yaml:"candidates"`
}

// Storage is the hard acquisition gate on free space (ADR-12).
type Storage struct {
	MinFreeBytes   Bytes `yaml:"min_free_bytes"`
	MinFreePercent int   `yaml:"min_free_percent"`
}

// Workers sizes the job pools (§3).
type Workers struct {
	Search   int `yaml:"search"`
	Acquire  int `yaml:"acquire"`
	Metadata int `yaml:"metadata"`
	// Import is fixed at 1 and is not configurable (ADR-3). It is present so
	// that setting it is a clear error rather than a silently ignored key.
	Import int `yaml:"import"`
}

// Logging controls the slog handler.
type Logging struct {
	Level  LogLevel  `yaml:"level"`
	Format LogFormat `yaml:"format"`
}

// Default returns the configuration a first run would produce. Every setting
// works untouched at this level (Invariant 38).
func Default() Config {
	return Config{
		Version: CurrentVersion,
		Server:  Server{Bind: "0.0.0.0", Port: 8080, BaseURL: "/"},
		Paths: Paths{
			ConfigDir: "/config",
			DataDir:   "/config/data",
			DBDir:     "/config/data/db",
			CacheDir:  "/config/data/cache",
			BackupDir: "/config/data/backups",
		},
		Security: Security{
			AuthMode:           AuthLocal,
			TrustedProxyHeader: "X-Remote-User",
		},
		Database: Database{
			Driver:      DriverModernc,
			BusyTimeout: Duration(15 * time.Second),
			PageSize:    4096,
			CacheSizeKB: 65536,
			WriteWarn:   Duration(50 * time.Millisecond),
			WriteHard:   Duration(2 * time.Second),
		},
		Backup: Backup{
			Schedule:         "0 3 * * *",
			SettingsOnChange: true,
			Keep:             5,
			KeepSettings:     20,
			BeforeMigration:  true,
		},
		Retention: Retention{
			DecisionsFull: Duration(2160 * time.Hour),
			Candidates:    Duration(168 * time.Hour),
		},
		Storage: Storage{
			MinFreeBytes:   Bytes(50 * 1000 * 1000 * 1000),
			MinFreePercent: 2,
		},
		Workers: Workers{Search: 4, Acquire: 2, Metadata: 4, Import: 1},
		Logging: Logging{Level: LevelInfo, Format: FormatJSON},
		Network: Network{PublicIPCheck: PublicIPCheck{
			Enabled:  true,
			Endpoint: DefaultPublicIPEndpoint,
			Interval: Duration(6 * time.Hour),
		}},
		Mode: ModeNormal,
	}
}
