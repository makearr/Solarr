package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/config"
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
		"server.port":              "MEDIANEXUS_SERVER_PORT",
		"paths.db_dir":             "MEDIANEXUS_PATHS_DB_DIR",
		"security.auth_mode":       "MEDIANEXUS_SECURITY_AUTH_MODE",
		"database.cache_size_kb":   "MEDIANEXUS_DATABASE_CACHE_SIZE_KB",
		"retention.decisions_full": "MEDIANEXUS_RETENTION_DECISIONS_FULL",
		"mode":                     "MEDIANEXUS_MODE",
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
		env("MEDIANEXUS_SERVER_PORT", "9999"))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Server.Port != 9999 {
		t.Errorf("Server.Port = %d, want the override 9999", parsed.Server.Port)
	}
}

func TestParseWithEnv_LeavesUnsetValuesAlone(t *testing.T) {
	parsed, err := config.ParseWithEnv(
		[]byte("version: 1\nserver:\n  port: 8080\n"),
		env("MEDIANEXUS_LOGGING_LEVEL", "debug"))
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
	_, fromEnv := config.ParseWithEnv([]byte("version: 1\n"), env("MEDIANEXUS_SERVER_PORT", "70000"))

	if fromFile == nil || fromEnv == nil {
		t.Fatalf("expected both to fail; file=%v env=%v", fromFile, fromEnv)
	}
	if fromFile.Error() != fromEnv.Error() {
		t.Errorf("an override reports differently from a file value:\n file: %s\n env:  %s", fromFile, fromEnv)
	}
}

func TestParseWithEnv_UnparseableOverrideNamesTheVariable(t *testing.T) {
	_, err := config.ParseWithEnv([]byte("version: 1\n"), env("MEDIANEXUS_SERVER_PORT", "eighty"))
	if err == nil {
		t.Fatal("a non-numeric port was accepted")
	}
	if !strings.Contains(err.Error(), "MEDIANEXUS_SERVER_PORT") {
		t.Errorf("the error does not name the variable that caused it:\n%s", err)
	}
}

func TestParseWithEnv_ParsesTypedValues(t *testing.T) {
	parsed, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"MEDIANEXUS_DATABASE_BUSY_TIMEOUT", "45s",
		"MEDIANEXUS_STORAGE_MIN_FREE_BYTES", "250GB",
		"MEDIANEXUS_RETENTION_DECISIONS_FULL", "30d",
		"MEDIANEXUS_MODE", "shadow",
		"MEDIANEXUS_BACKUP_SETTINGS_ON_CHANGE", "false",
		"MEDIANEXUS_SECURITY_TRUSTED_PROXY_CIDRS", "[10.0.0.0/8, 192.168.0.0/16]",
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
		"MEDIANEXUS_CONFIG_DIR", "/srv/config",
		"MEDIANEXUS_DB_DIR", "/srv/db",
		"MEDIANEXUS_DATA_DIR", "/srv/data",
		"MEDIANEXUS_CACHE_DIR", "/srv/cache",
		"MEDIANEXUS_BACKUP_DIR", "/srv/backups",
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
		"MEDIANEXUS_DB_DIR", "/srv/db",
		"MEDIANEXUS_PATHS_DB_DIR", "/other/db",
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
		"MEDIANEXUS_DB_DIR", "/srv/db",
		"MEDIANEXUS_PATHS_DB_DIR", "/srv/db",
	))
	if err != nil {
		t.Fatalf("ParseWithEnv: %v", err)
	}
	if parsed.Paths.DBDir != "/srv/db" {
		t.Errorf("Paths.DBDir = %q, want /srv/db", parsed.Paths.DBDir)
	}
}

func TestParseWithEnv_AMalformedSecretDoesNotEchoTheValue(t *testing.T) {
	// A YAML parser quotes the offending source text back, so redacting the
	// Value field alone leaves the secret in the explanation (Invariant 61).
	_, err := config.ParseWithEnv([]byte("version: 1\n"), env(
		"MEDIANEXUS_SECURITY_ENCRYPTION_KEY", "[S3CR3T: valid: yaml",
	))
	if err == nil {
		t.Fatal("the malformed key was accepted")
	}
	if strings.Contains(err.Error(), "S3CR3T") {
		t.Errorf("the encryption key appeared in the error:\n%s", err)
	}
}
