package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/platform/config"
)

func env(pairs ...string) config.Lookup {
	values := make(map[string]string, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		values[pairs[index]] = pairs[index+1]
	}
	return func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
}

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		"server.port":              "SOLARR_SERVER_PORT",
		"paths.db_dir":             "SOLARR_PATHS_DB_DIR",
		"security.auth_mode":       "SOLARR_SECURITY_AUTH_MODE",
		"database.cache_size_kb":   "SOLARR_DATABASE_CACHE_SIZE_KB",
		"retention.decisions_full": "SOLARR_RETENTION_DECISIONS_FULL",
		"mode":                     "SOLARR_MODE",
	}
	for path, want := range cases {
		if got := config.EnvName(path); got != want {
			t.Errorf("EnvName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestParseWithEnv_OverridesAFileValue(t *testing.T) {
	parsed, err := config.ParseWithEnv(
		[]byte("version: 1\nserver:\n  port: 8080\n"),
		env("SOLARR_SERVER_PORT", "9999"))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Server.Port != 9999 {
		t.Errorf("Server.Port = %d, want the override 9999", parsed.Server.Port)
	}
}

// A string override is literal text, not YAML source: a value starting with a
// YAML-special character (here *, a cron wildcard and also YAML's alias-reference
// syntax) must not be misread as document syntax. Found live: SOLARR_BACKUP_
// SCHEDULE="* * * * *" crashed startup with "undefined alias name" before this was
// fixed to assign string-kind fields directly rather than route them through
// yaml.Unmarshal.
func TestParseWithEnv_ACronScheduleStartingWithAsteriskIsNotYAML(t *testing.T) {
	parsed, err := config.ParseWithEnv(
		[]byte("version: 1\n"),
		env("SOLARR_BACKUP_SCHEDULE", "* * * * *"))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Backup.Schedule != "* * * * *" {
		t.Errorf("Backup.Schedule = %q, want the literal override", parsed.Backup.Schedule)
	}
}

// The asterisk is not special-cased; no YAML-special leading character is. Uses a
// field with no format validation of its own (unlike backup.schedule's cron check
// above), so this is purely about the raw text reaching the field unmangled.
func TestParseWithEnv_OtherYAMLSpecialCharactersAreAlsoLiteral(t *testing.T) {
	cases := []string{"&anchor-like", "!tag-like", "[not-a-list]", "{not-a-map}"}
	for _, value := range cases {
		parsed, err := config.ParseWithEnv(
			[]byte("version: 1\n"), env("SOLARR_SECURITY_TRUSTED_PROXY_HEADER", value))
		if err != nil {
			t.Fatalf("ParseWithEnv(%q): %v", value, err)
		}
		if parsed.Security.TrustedProxyHeader != value {
			t.Errorf("TrustedProxyHeader = %q, want the literal %q", parsed.Security.TrustedProxyHeader, value)
		}
	}
}

func TestParseWithEnv_LeavesUnsetValuesAlone(t *testing.T) {
	parsed, err := config.ParseWithEnv(
		[]byte("version: 1\nserver:\n  port: 8080\n"),
		env("SOLARR_LOGGING_LEVEL", "debug"))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want the file's 8080", parsed.Server.Port)
	}
	if parsed.Logging.Level != config.LevelDebug {
		t.Errorf("Logging.Level = %q, want debug", parsed.Logging.Level)
	}
}

// ADR-44: an override is validated identically to a file value.
func TestParseWithEnv_AnInvalidOverrideFailsLikeAFileValue(t *testing.T) {
	_, fromFile := config.Parse([]byte("version: 1\nserver:\n  port: 70000\n"))
	_, fromEnv := config.ParseWithEnv([]byte("version: 1\n"), env("SOLARR_SERVER_PORT", "70000"))

	if fromFile == nil || fromEnv == nil {
		t.Fatalf("expected both to fail; file=%v env=%v", fromFile, fromEnv)
	}
	if fromFile.Error() != fromEnv.Error() {
		t.Errorf("an override reports differently from a file value:\n file: %s\n env:  %s", fromFile, fromEnv)
	}
}

func TestParseWithEnv_UnparseableOverrideNamesTheVariable(t *testing.T) {
	_, err := config.ParseWithEnv([]byte("version: 1\n"), env("SOLARR_SERVER_PORT", "eighty"))
	if err == nil {
		t.Fatal("a non-numeric port was accepted")
	}
	if !strings.Contains(err.Error(), "SOLARR_SERVER_PORT") {
		t.Errorf("the error does not name the variable that caused it:\n%s", err)
	}
}

func TestParseWithEnv_ParsesTypedValues(t *testing.T) {
	parsed, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"SOLARR_DATABASE_BUSY_TIMEOUT", "45s",
		"SOLARR_STORAGE_MIN_FREE_BYTES", "250GB",
		"SOLARR_RETENTION_DECISIONS_FULL", "30d",
		"SOLARR_MODE", "shadow",
		"SOLARR_BACKUP_SETTINGS_ON_CHANGE", "false",
		"SOLARR_SECURITY_TRUSTED_PROXY_CIDRS", "[10.0.0.0/8, 192.168.0.0/16]",
	))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}

	if got, want := parsed.Database.BusyTimeout.AsDuration(), 45*time.Second; got != want {
		t.Errorf("BusyTimeout = %v, want %v", got, want)
	}
	if got, want := parsed.Storage.MinFreeBytes.AsInt64(), int64(250_000_000_000); got != want {
		t.Errorf("MinFreeBytes = %d, want %d", got, want)
	}
	if got, want := parsed.Retention.DecisionsFull.AsDuration(), 30*24*time.Hour; got != want {
		t.Errorf("DecisionsFull = %v, want %v", got, want)
	}
	if parsed.Mode != config.ModeShadow {
		t.Errorf("Mode = %q, want shadow", parsed.Mode)
	}
	if parsed.Backup.SettingsOnChange {
		t.Error("SettingsOnChange = true, want the override's false")
	}
	if len(parsed.Security.TrustedProxyCIDRs) != 2 {
		t.Errorf("TrustedProxyCIDRs = %v, want two entries", parsed.Security.TrustedProxyCIDRs)
	}
}

// §14 documents the short names for the directory settings, because those are
// what a Docker deployment actually sets.
func TestParseWithEnv_DocumentedPathAliasesWork(t *testing.T) {
	parsed, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"SOLARR_CONFIG_DIR", "/srv/config",
		"SOLARR_DB_DIR", "/srv/db",
		"SOLARR_DATA_DIR", "/srv/data",
		"SOLARR_CACHE_DIR", "/srv/cache",
		"SOLARR_BACKUP_DIR", "/srv/backups",
	))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Paths.DBDir != "/srv/db" {
		t.Errorf("Paths.DBDir = %q, want /srv/db", parsed.Paths.DBDir)
	}
	if parsed.Paths.ConfigDir != "/srv/config" {
		t.Errorf("Paths.ConfigDir = %q, want /srv/config", parsed.Paths.ConfigDir)
	}
}

func TestParseWithEnv_ConflictingAliasAndCanonicalNameIsAnError(t *testing.T) {
	// A container with two variables disagreeing about where the database lives
	// should say so, not silently pick one.
	_, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"SOLARR_DB_DIR", "/srv/db",
		"SOLARR_PATHS_DB_DIR", "/other/db",
	))
	if err == nil {
		t.Fatal("two variables disagreeing about paths.db_dir were accepted")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("the error does not explain the conflict:\n%s", err)
	}
}

func TestParseWithEnv_AgreeingAliasAndCanonicalNameIsFine(t *testing.T) {
	parsed, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"SOLARR_DB_DIR", "/srv/db",
		"SOLARR_PATHS_DB_DIR", "/srv/db",
	))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Paths.DBDir != "/srv/db" {
		t.Errorf("Paths.DBDir = %q, want /srv/db", parsed.Paths.DBDir)
	}
}

// A malformed encryption key set via env var passes ParseWithEnv unrejected --
// consistent with the file path, which never validated the key's format at parse
// time either. What Invariant 61 actually requires -- the secret never appears in
// an error -- is DecodeKey's job at first use (serve.go), and it redacts
// unconditionally regardless of what made the key invalid.
func TestParseWithEnv_AMalformedSecretPassesThroughLiterally(t *testing.T) {
	parsed, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"SOLARR_SECURITY_ENCRYPTION_KEY", "S3CR3T-not-a-valid-key",
	))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Security.EncryptionKey != "S3CR3T-not-a-valid-key" {
		t.Errorf("EncryptionKey = %q, want the literal override", parsed.Security.EncryptionKey)
	}

	_, decodeErr := config.DecodeKey(parsed.Security.EncryptionKey)
	if decodeErr == nil {
		t.Fatal("DecodeKey accepted a malformed key")
	}
	if strings.Contains(decodeErr.Error(), "S3CR3T") {
		t.Errorf("the encryption key appeared in DecodeKey's error:\n%s", decodeErr)
	}
}
