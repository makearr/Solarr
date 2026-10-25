package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/makearr/Solarr/internal/platform/fsx"
)

// keyPrefix marks how the encryption key is encoded, so a future change of
// encoding is distinguishable rather than a silent decode failure.
const keyPrefix = "base64:"

// keyBytes is 256 bits, which is what the AEAD used for settings.db wants.
const keyBytes = 32

// ErrAlreadyExists reports a config.yml that is already there.
var ErrAlreadyExists = errors.New("config: file already exists")

// GenerateKey makes a new encryption key (ADR-14).
func GenerateKey() (string, error) {
	raw := make([]byte, keyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("config: generate encryption key: %w", err)
	}
	return keyPrefix + base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeKey returns the raw key bytes.
func DecodeKey(key string) ([]byte, error) {
	encoded, found := strings.CutPrefix(key, keyPrefix)
	if !found {
		// The value is not echoed: it is the key (Invariant 61).
		return nil, errors.New("config: encryption key is not in the expected `base64:` form")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("config: encryption key is not valid base64")
	}
	if len(raw) != keyBytes {
		return nil, fmt.Errorf("config: encryption key is %d bytes, want %d", len(raw), keyBytes)
	}
	return raw, nil
}

// Create writes a first-run config.yml with a freshly generated encryption key and returns the
// configuration it represents.
func Create(fsys fsx.FS, path string, generateKey func() (string, error), lookup Lookup) (Config, error) {
	if _, err := fsys.Lstat(path); err == nil {
		return Config{}, fmt.Errorf("%w: %s", ErrAlreadyExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("config: check %s: %w", path, err)
	}

	key, err := generateKey()
	if err != nil {
		return Config{}, err
	}

	document := fmt.Sprintf(template, CurrentVersion, key)

	// Asks only whether the template still matches the schema, so no overrides.
	if _, err := Parse([]byte(document)); err != nil {
		return Config{}, fmt.Errorf("config: the generated file is not valid: %w", err)
	}

	// The section 14.3 checks that normally create it run after this.
	directory := filepath.Dir(path)
	if err := fsys.MkdirAll(directory, directoryPermissions); err != nil {
		return Config{}, fmt.Errorf("config: create %s: %w (%s)", directory, err, identityHint())
	}

	// Secret, not plain: this carries the only copy of the encryption key.
	if err := fsx.WriteAtomicSecretNew(fsys, path, func(w io.Writer) error {
		_, writeErr := io.WriteString(w, document)
		return writeErr
	}); err != nil {
		return Config{}, fmt.Errorf("config: write %s: %w", path, err)
	}

	// As Load does, or a first run ignores every override and then succeeds on
	// the next, which is far harder to diagnose than a plain failure.
	created, err := ParseWithEnv([]byte(document), lookup)
	if err != nil {
		return Config{}, err
	}
	return created, nil
}

// template is hand-written rather than marshalled from Default, because a bootstrap file a
// human edits deserves comments explaining what each setting is for.
const template = `# Solarr bootstrap configuration.
#
# Only what must be known before a database can be opened lives here. Everything
# else — indexers, download clients, quality profiles, categories, root folders —
# is configured in the web interface and stored in settings.db.
#
# Every setting can also be given as an environment variable:
#   server.port  ->  SOLARR_SERVER_PORT
# The five directories additionally accept the short forms SOLARR_CONFIG_DIR,
# SOLARR_DATA_DIR, SOLARR_DB_DIR, SOLARR_CACHE_DIR and
# SOLARR_BACKUP_DIR.
#
# Run 'solarr config check' to validate this file without starting up.

# Schema version of this file, not the application version. Do not edit.
version: %d

server:
  bind: 0.0.0.0
  port: 5454
  base_url: /

# Defaults nest under config_dir, so mounting /config alone is enough.
paths:
  config_dir: /config
  data_dir: /config/data
  db_dir: /config/data/db
  cache_dir: /config/data/cache
  backup_dir: /config/data/backups

security:
  # Encrypts credentials in settings.db. Generated on first run.
  #
  # Keep it. Losing it means re-entering every indexer and download client
  # password. It is not protection against someone with access to this file —
  # it protects database backups, exports and support bundles, which do not
  # carry this file.
  encryption_key: %s

  # local | proxy | apikey_only | none
  #
  # none serves every request as the administrator and asks for no credential at
  # all. Only ever appropriate where something else already restricts who can
  # reach the port. Startup says so, loudly, every time.
  auth_mode: local

  # Used only when auth_mode is proxy. Setting proxy without trusted_proxy_cidrs
  # would let anyone claim to be any user, so it is refused.
  trusted_proxy_header: X-Remote-User
  trusted_proxy_cidrs: []

database:
  busy_timeout: 15s
  # Applied when a database is created and fixed thereafter.
  page_size: 4096
  # Sizes library.db, which is the only one that grows. jobs.db and settings.db take
  # smaller sizes from their role -- SQLite's cache is per connection, so one figure
  # for all three is a ceiling several times what it looks like.
  cache_size_kb: 65536
  # A write transaction over write_warn is logged; over write_hard is an error.
  write_warn: 50ms
  write_hard: 2s

backup:
  # Routine backups and their cleanup are set on the Scheduler screen.
  # settings.db is tiny and holds the credentials, so it is snapshot on change.
  settings_on_change: true
  keep_settings: 20
  # Migrations are forward-only; this backup is the only rollback. Not optional.
  before_migration: true

retention:
  # Full decision detail, then aggregated.
  decisions_full: 90d
  candidates: 168h

storage:
  # Acquisition stops below either threshold.
  min_free_bytes: 50GB
  min_free_percent: 2

workers:
  search: 4
  acquire: 2
  metadata: 4
  # Fixed at 1. One importer at a time is what keeps write latency bounded.
  import: 1

network:
  # Off by default: the only call to a host you did not configure. It asks a
  # third party what address this host connects from, which tells you whether
  # traffic is leaving past a VPN.
  public_ip_check:
    enabled: false
    endpoint: https://api.ipify.org
    interval: 6h

logging:
  # debug | info | warn | error
  level: info
  # json | text
  format: json

# normal  — act on decisions
# observe — decide and record, never act
# shadow  — act on everything except the irreversible steps
mode: normal
`
