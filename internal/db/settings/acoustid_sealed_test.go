package settings

import (
	"bytes"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/platform/secrets"
)

// The round trip in acoustid_test.go proves the key comes back. This proves it
// was never written in the clear on the way in (ADR-14). Inside the package
// because sealedForStorage is the step that decides, and exposing it only for
// a test would be worse than testing it where it lives.
func TestSealedForStorage_NeverContainsThePlainKey(t *testing.T) {
	cipher, err := secrets.New(bytes.Repeat([]byte{0x2a}, 32))
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	const key = "a-real-acoustid-key"

	stored, err := sealedForStorage(cipher, key)
	if err != nil {
		t.Fatalf("sealedForStorage: %v", err)
	}
	if stored == "" {
		t.Fatal("sealing produced nothing")
	}
	if strings.Contains(stored, key) {
		t.Errorf("the stored form contains the key in plain text: %q", stored)
	}
}
