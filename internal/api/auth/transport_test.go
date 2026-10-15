package auth_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/api/auth"
)

// sealFixed seals as the browser does (web/src/api/seal.ts), with a fixed ephemeral key and nonce so the result is a
// known vector both sides can check.
func sealFixed(t *testing.T, serverPublic string, ephemeralSeed, nonce []byte, plain string) string {
	t.Helper()
	ephemeral, err := ecdh.X25519().NewPrivateKey(ephemeralSeed)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, err := base64.StdEncoding.DecodeString(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ecdh.X25519().NewPublicKey(publicBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := ephemeral.ECDH(server)
	if err != nil {
		t.Fatal(err)
	}
	key, err := hkdf.Key(sha256.New, shared, nil, "solarr:transport-seal:v1", 32)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	encode := base64.StdEncoding.EncodeToString
	return auth.SealedPrefix + encode(ephemeral.PublicKey().Bytes()) + ":" + encode(nonce) + ":" + encode(aead.Seal(nil, nonce, []byte(plain), nil))
}

func fixedTransport(t *testing.T) *auth.Transport {
	t.Helper()
	transport, err := auth.NewTransport([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

// The vector web/src/api/seal.test.ts produces from the same inputs: the browser and the server agree byte for byte.
const (
	vectorPublicKey = "16eh9YxjRWIIP4Hj/YvbagfDZOl7AhAMVRbtMuIdcFY="
	vectorSealed    = "encv1:+0Gx/PdcEif6CIdgVqXtB6xE/gwRbsLegLjlI5WdeCQ=:AAECAwQFBgcICQoL:RnnyFiO0hEc0zyOfISH9F/sfrgpweUYSyHiNdfA="
)

func TestTransport_OpensWhatTheBrowserSealed(t *testing.T) {
	transport := fixedTransport(t)
	seed := []byte(strings.Repeat("e", 32))
	nonce := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	sealed := sealFixed(t, transport.PublicKey(), seed, nonce, "a secret pass")
	if transport.PublicKey() != vectorPublicKey || sealed != vectorSealed {
		t.Errorf("public key %s, sealed %s; want the shared vector", transport.PublicKey(), sealed)
	}
	if plain, err := transport.Open(sealed); err != nil || plain != "a secret pass" {
		t.Errorf("Open = %q, %v; want the secret", plain, err)
	}
}

func TestTransport_RefusesWhatDoesNotOpen(t *testing.T) {
	transport := fixedTransport(t)
	other, err := auth.NewTransport([]byte("another key, another server....."))
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, 12)
	good := sealFixed(t, transport.PublicKey(), []byte(strings.Repeat("e", 32)), nonce, "secret")
	for name, value := range map[string]string{
		"for another server": sealFixed(t, other.PublicKey(), []byte(strings.Repeat("e", 32)), nonce, "secret"),
		"tampered":           good[:len(good)-4] + "AAAA",
		"too few parts":      "encv1:abc:def",
		"not base64":         "encv1:!!:!!:!!",
		"no prefix":          strings.TrimPrefix(good, auth.SealedPrefix),
	} {
		if _, err := transport.Open(value); !errors.Is(err, auth.ErrUnsealable) {
			t.Errorf("%s: Open err = %v, want ErrUnsealable", name, err)
		}
	}
}
