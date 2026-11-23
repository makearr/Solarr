package webhook

import (
	"bytes"
	"testing"
)

func TestTokenMatchesOnlyTheTokenItWasMadeFrom(t *testing.T) {
	token, hash, err := NewToken(bytes.NewReader(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Fatalf("token %q is %d characters, want 43 (32 bytes, base64url)", token, len(token))
	}
	if hash == token || len(hash) != 64 {
		t.Fatalf("hash %q must be the SHA-256 in hex, not the token", hash)
	}
	if !TokenMatches(hash, token) {
		t.Error("the token does not match its own hash")
	}
	for _, wrong := range []string{"", token + "x", token[:len(token)-1], hash} {
		if TokenMatches(hash, wrong) {
			t.Errorf("%q matched", wrong)
		}
	}
}

func TestNewTokenFailsOnAShortRandomSource(t *testing.T) {
	if _, _, err := NewToken(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Fatal("want an error")
	}
}
