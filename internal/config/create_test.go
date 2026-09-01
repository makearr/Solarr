package config_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/clock"
	"github.com/makearr/MediaNexus/internal/config"
	"github.com/makearr/MediaNexus/internal/fsx"
)

func fixedKey() (string, error) { return "base64:" + strings.Repeat("A", 43) + "=", nil }

func newConfigFS(t *testing.T) fsx.FS {
	t.Helper()
	fsys := fsx.NewMem(clock.NewFakeAtEpoch())
	if err := fsys.MkdirAll("/config", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	return fsys
}

func readAll(t *testing.T, fsys fsx.FS, path string) string {
	t.Helper()
	file, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// The template is hand-written for its comments, so it can drift from the
// schema. If it ever does, a first run writes a file that will not load.
func TestCreate_MatchesTheDefaults(t *testing.T) {
	fsys := newConfigFS(t)

	created, err := config.Create(fsys, "/config/config.yml", fixedKey)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	want := config.Default()
	want.Security.EncryptionKey = created.Security.EncryptionKey
	want.Security.TrustedProxyCIDRs = created.Security.TrustedProxyCIDRs

	if created.Server != want.Server {
		t.Errorf("Server = %+v, want %+v", created.Server, want.Server)
	}
	if created.Paths != want.Paths {
		t.Errorf("Paths = %+v, want %+v", created.Paths, want.Paths)
	}
	if created.Database != want.Database {
		t.Errorf("Database = %+v, want %+v", created.Database, want.Database)
	}
	if created.Backup != want.Backup {
		t.Errorf("Backup = %+v, want %+v", created.Backup, want.Backup)
	}
	if created.Retention != want.Retention {
		t.Errorf("Retention = %+v, want %+v", created.Retention, want.Retention)
	}
	if created.Storage != want.Storage {
		t.Errorf("Storage = %+v, want %+v", created.Storage, want.Storage)
	}
	if created.Workers != want.Workers {
		t.Errorf("Workers = %+v, want %+v", created.Workers, want.Workers)
	}
	if created.Logging != want.Logging {
		t.Errorf("Logging = %+v, want %+v", created.Logging, want.Logging)
	}
	if created.Mode != want.Mode {
		t.Errorf("Mode = %q, want %q", created.Mode, want.Mode)
	}
	if created.Version != config.CurrentVersion {
		t.Errorf("Version = %d, want %d", created.Version, config.CurrentVersion)
	}
}

func TestCreate_WritesAFileThatLoadsBack(t *testing.T) {
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := config.Load(fsys, "/config/config.yml")
	if err != nil {
		t.Fatalf("Load of the generated file: %v", err)
	}
	if loaded.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080", loaded.Server.Port)
	}
}

func TestCreate_RefusesToOverwrite(t *testing.T) {
	// The file carries the only copy of the key; a second one would make every
	// credential in settings.db undecryptable with no error until one was read.
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	before := readAll(t, fsys, "/config/config.yml")

	_, err := config.Create(fsys, "/config/config.yml", fixedKey)
	if !errors.Is(err, config.ErrAlreadyExists) {
		t.Fatalf("second Create = %v, want ErrAlreadyExists", err)
	}
	if after := readAll(t, fsys, "/config/config.yml"); after != before {
		t.Error("the existing file was modified")
	}
}

func TestCreate_GeneratesADistinctKeyEachTime(t *testing.T) {
	first, err := config.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	second, err := config.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if first == second {
		t.Fatal("two generated keys are identical")
	}

	raw, err := config.DecodeKey(first)
	if err != nil {
		t.Fatalf("DecodeKey: %v", err)
	}
	if len(raw) != 32 {
		t.Errorf("key is %d bytes, want 32", len(raw))
	}
}

func TestDecodeKey_RejectsMalformedKeysWithoutEchoingThem(t *testing.T) {
	cases := []string{
		"",
		"S3CR3T-not-prefixed",
		"base64:S3CR3T-not-base64!!!",
		"base64:c2hvcnQ=", // valid base64, wrong length
	}
	for _, key := range cases {
		_, err := config.DecodeKey(key)
		if err == nil {
			t.Errorf("DecodeKey(%q) = nil, want an error", key)
			continue
		}
		if strings.Contains(err.Error(), "S3CR3T") {
			t.Errorf("DecodeKey echoed the key: %v", err)
		}
	}
}

func TestCreate_LeavesNoTemporaryBehind(t *testing.T) {
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := fsys.ReadDir("/config")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if fsx.IsTemporary(entry.Name) {
			t.Errorf("a temporary %q was left behind", entry.Name)
		}
	}
}

func TestCreate_TemplateDocumentsTheEnvironmentOverrides(t *testing.T) {
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey); err != nil {
		t.Fatalf("Create: %v", err)
	}
	document := readAll(t, fsys, "/config/config.yml")

	for _, want := range []string{"MEDIANEXUS_SERVER_PORT", "MEDIANEXUS_DB_DIR", "medianexus config check"} {
		if !strings.Contains(document, want) {
			t.Errorf("the generated file does not mention %q", want)
		}
	}
}
