package config_test

import (
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/config"
)

// Against the real shipped template (create.go's own document, not a
// miniature stand-in), using the same newConfigFS/fixedKey/readAll helpers
// create_test.go already established.
func TestSetServerPort_RoundTripsThroughTheRealShippedTemplate(t *testing.T) {
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey, noEnv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	original := readAll(t, fsys, "/config/config.yml")

	patched, err := config.SetServerPort([]byte(original), 9999)
	if err != nil {
		t.Fatalf("SetServerPort: %v", err)
	}
	text := string(patched)

	if !strings.Contains(text, "port: 9999") {
		t.Errorf("patched document does not contain the new port:\n%s", text)
	}
	// A spot check of comment text unique to the real template -- proof this
	// ran against the actual shipped document, not a stand-in, and that the
	// comment survived a full parse-and-reserialise round trip.
	if !strings.Contains(text, "Run 'solarr config check'") {
		t.Error("patched document lost a comment from the real template")
	}
	if _, err := config.Parse(patched); err != nil {
		t.Errorf("patched document does not parse: %v", err)
	}
}

const patchTestDoc = `# a leading comment
version: 1

server:
  bind: 0.0.0.0
  port: 5454
  base_url: /

security:
  encryption_key: base64:abc
`

func TestSetServerPort_ChangesOnlyThePortLine(t *testing.T) {
	patched, err := config.SetServerPort([]byte(patchTestDoc), 9999)
	if err != nil {
		t.Fatalf("SetServerPort: %v", err)
	}
	text := string(patched)

	if !strings.Contains(text, "port: 9999") {
		t.Errorf("patched document does not contain the new port:\n%s", text)
	}
	if strings.Contains(text, "port: 5454") {
		t.Errorf("patched document still contains the old port:\n%s", text)
	}

	// Everything else -- the leading comment, bind, base_url, the security
	// section -- must survive untouched. A full struct remarshal would have
	// dropped the comment and possibly reordered fields; this is the
	// guarantee a hand-edited bootstrap file needs.
	for _, unchanged := range []string{
		"# a leading comment", "bind: 0.0.0.0", "base_url: /", "encryption_key: base64:abc",
	} {
		if !strings.Contains(text, unchanged) {
			t.Errorf("patched document lost %q:\n%s", unchanged, text)
		}
	}

	// The result must still parse as valid config.
	if _, err := config.Parse(patched); err != nil {
		t.Errorf("patched document does not parse: %v", err)
	}
}

func TestSetServerPort_RefusesUnparsableYAML(t *testing.T) {
	if _, err := config.SetServerPort([]byte("not: valid: yaml: at: all: ["), 9999); err == nil {
		t.Error("SetServerPort on unparsable YAML = nil error, want one")
	}
}

func TestSetServerPort_RefusesADocumentWithNoServerSection(t *testing.T) {
	if _, err := config.SetServerPort([]byte("version: 1\n"), 9999); err == nil {
		t.Error("SetServerPort on a document with no server.port = nil error, want one")
	}
}

func TestSetDatabaseCacheSizeKB_ChangesOnlyThatLineOfTheShippedTemplate(t *testing.T) {
	fsys := newConfigFS(t)
	if _, err := config.Create(fsys, "/config/config.yml", fixedKey, noEnv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	original := readAll(t, fsys, "/config/config.yml")

	patched, err := config.SetDatabaseCacheSizeKB([]byte(original), 131072)
	if err != nil {
		t.Fatalf("SetDatabaseCacheSizeKB: %v", err)
	}
	want := strings.Replace(original, "cache_size_kb: 65536", "cache_size_kb: 131072", 1)
	if string(patched) != want {
		t.Errorf("patched document differs from the original in more than the one line:\n%s", patched)
	}
}
