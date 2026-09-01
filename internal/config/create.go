package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/makearr/MediaNexus/internal/fsx"
)

// keyPrefix marks how the encryption key is encoded, so a future change of
// encoding is distinguishable rather than a silent decode failure.
const keyPrefix = "base64:"

// keyBytes is 256 bits, which is what the AEAD used for settings.db wants.
const keyBytes = 32

// ErrAlreadyExists reports a config.yml that is already there. Creation never
// overwrites: the file holds the encryption key, and replacing it makes every
// stored credential undecryptable.
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

// Create writes a first-run config.yml with a freshly generated encryption key
// and returns the configuration it represents.
//
// It refuses to overwrite. The file carries the only copy of the key, and a
// second one would make every credential in settings.db undecryptable with no
// error until something tried to read one.
func Create(fsys fsx.FS, path string, generateKey func() (string, error), lookup Lookup) (Config, error) {
	if _, err := fsys.Stat(path); err == nil {
		return Config{}, fmt.Errorf("%w: %s", ErrAlreadyExists, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("config: check %s: %w", path, err)
	}

	key, err := generateKey()
	if err != nil {
		return Config{}, err
	}

	document := fmt.Sprintf(template, CurrentVersion, key)

	// The template is validated on its own before anything is written, so a
	// template that has drifted from the schema fails without leaving a file on
	// disk that will not load next time. Environment overrides are deliberately
	// not applied to this pass: this asks "did we generate a valid file", and a
	// bad override is not an answer to that question.
	if _, err := Parse([]byte(document)); err != nil {
		return Config{}, fmt.Errorf("config: the generated file is not valid: %w", err)
	}

	// WriteAtomicSecret, not WriteAtomic: this document carries the only copy of
	// the encryption key, so it is created 0600 rather than world-readable.
	if err := fsx.WriteAtomicSecret(fsys, path, func(w io.Writer) error {
		_, writeErr := io.WriteString(w, document)
		return writeErr
	}); err != nil {
		return Config{}, fmt.Errorf("config: write %s: %w", path, err)
	}

	// The environment is applied to what is returned, exactly as Load does it.
	// Without this a first run ignores every override and refuses to start on
	// the default paths, then succeeds on the next run because the file now
	// exists -- a container that fails once and then fixes itself, which is far
	// harder to diagnose than one that simply fails.
	created, err := ParseWithEnv([]byte(document), lookup)
	if err != nil {
		return Config{}, err
	}
	return created, nil
}

// template is hand-written rather than marshalled from Default, because a
// bootstrap file a human edits deserves comments explaining what each setting is
// for. TestCreate_MatchesTheDefaults asserts the two cannot drift apart.
const template = `# MediaNexus bootstrap configuration.
#
# Only what must be known before a database can be opened lives here. Everything
# else — indexers, download clients, quality profiles, categories, root folders —
# is configured in the web interface and stored in settings.db.
#
# Every setting can also be given as an environment variable:
#   server.port  ->  MEDIANEXUS_SERVER_PORT
# The five directories additionally accept the short forms MEDIANEXUS_CONFIG_DIR,
# MEDIANEXUS_DATA_DIR, MEDIANEXUS_DB_DIR, MEDIANEXUS_CACHE_DIR and
# MEDIANEXUS_BACKUP_DIR.
#
# Run 'medianexus config check' to validate this file without starting up.

# Schema version of this file, not the application version. Do not edit.
version: %d

server:
  bind: 0.0.0.0
  port: 8080
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

  # local | proxy | apikey_only
  auth_mode: local

  # Used only when auth_mode is proxy. Setting proxy without trusted_proxy_cidrs
  # would let anyone claim to be any user, so it is refused.
  trusted_proxy_header: X-Remote-User
  trusted_proxy_cidrs: []

database:
  driver: modernc
  busy_timeout: 15s
  # Applied when a database is created and fixed thereafter.
  page_size: 4096
  cache_size_kb: 65536
  # A write transaction over write_warn is logged; over write_hard is an error.
  write_warn: 50ms
  write_hard: 2s

backup:
  schedule: "0 3 * * *"
  # settings.db is tiny and holds the credentials, so it is snapshot on change.
  settings_on_change: true
  keep: 5
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
