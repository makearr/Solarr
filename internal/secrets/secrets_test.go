package secrets_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/secrets"
)

const password = "hunter2-correct-horse-battery-staple"

func testKey() []byte { return bytes.Repeat([]byte{0x2a}, 32) }

// A convention would be forgotten; a type that cannot be printed will not be.
// These cover every route a string normally takes out of the process.
func TestSecret_CannotBePrintedByAccident(t *testing.T) {
	secret := secrets.Secret(password)

	// staticcheck suggests secret.String() for the %s case. The point is to
	// exercise the fmt route a caller would actually take by accident, not the
	// method it happens to reach.
	cases := map[string]string{
		"%s":       fmt.Sprintf("%s", secret), //nolint:staticcheck // the verb is the test
		"%v":       fmt.Sprintf("%v", secret),
		"%#v":      fmt.Sprintf("%#v", secret),
		"%q":       fmt.Sprintf("%q", secret),
		"Println":  fmt.Sprint(secret),
		"in error": fmt.Errorf("connecting with %v", secret).Error(),
		"in struct": fmt.Sprintf("%v", struct {
			Name  string
			Token secrets.Secret
		}{Name: "indexer", Token: secret}),
	}

	for route, rendered := range cases {
		if strings.Contains(rendered, "hunter2") {
			t.Errorf("%s leaked the secret: %s", route, rendered)
		}
	}
}

func TestSecret_DoesNotReachTheLog(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))

	logger.Info("connecting", slog.Any("api_key", secrets.Secret(password)))

	if strings.Contains(buffer.String(), "hunter2") {
		t.Errorf("slog wrote the secret: %s", buffer.String())
	}
}

func TestSecret_DoesNotReachAnAPIResponse(t *testing.T) {
	// A secret must not escape by being embedded in a struct someone forgot to
	// filter before marshalling.
	encoded, err := json.Marshal(struct {
		Name   string         `json:"name"`
		APIKey secrets.Secret `json:"api_key"`
	}{Name: "nzbgeek", APIKey: secrets.Secret(password)})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(encoded), "hunter2") {
		t.Errorf("JSON encoding leaked the secret: %s", encoded)
	}
}

func TestSecret_RevealReturnsTheValue(t *testing.T) {
	if got := secrets.Secret(password).Reveal(); got != password {
		t.Errorf("Reveal() = %q, want the original value", got)
	}
}

func TestSecret_Mask(t *testing.T) {
	cases := []struct {
		name     string
		secret   string
		hasValue bool
		masked   string
	}{
		{"absent", "", false, ""},
		{"long enough to hint", "sk-abcdefghijklmnop", true, "sk-…nop"},
		// Revealing three of six characters is most of a six-character secret.
		{"too short to hint", "abcdef", true, "••••••"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := secrets.Secret(testCase.secret).Mask()
			if got.HasValue != testCase.hasValue {
				t.Errorf("HasValue = %v, want %v", got.HasValue, testCase.hasValue)
			}
			if got.Masked != testCase.masked {
				t.Errorf("Masked = %q, want %q", got.Masked, testCase.masked)
			}
		})
	}
}

func TestCipher_RoundTrips(t *testing.T) {
	cipher, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sealed, err := cipher.Seal(secrets.Secret(password))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Error("the ciphertext contains the plaintext")
	}

	opened, err := cipher.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Reveal() != password {
		t.Errorf("round trip produced %q", opened.Reveal())
	}
}

// Otherwise a settings.db would reveal which accounts share a password.
func TestCipher_SealingTheSameValueTwiceDiffers(t *testing.T) {
	cipher, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	first, err := cipher.Seal(secrets.Secret(password))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := cipher.Seal(secrets.Secret(password))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of one value are identical; the nonce is not fresh")
	}

	for index, sealed := range [][]byte{first, second} {
		opened, err := cipher.Open(sealed)
		if err != nil {
			t.Fatalf("Open %d: %v", index, err)
		}
		if opened.Reveal() != password {
			t.Errorf("Open %d produced %q", index, opened.Reveal())
		}
	}
}

func TestCipher_RejectsTamperedCiphertext(t *testing.T) {
	cipher, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := cipher.Seal(secrets.Secret(password))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 0x01

	// Authenticated encryption: a flipped bit fails rather than decrypting to
	// rubbish that then gets sent to an indexer as a password.
	if _, err := cipher.Open(tampered); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("Open of tampered ciphertext = %v, want ErrCorrupt", err)
	}
}

func TestCipher_RejectsAValueFromAnotherKey(t *testing.T) {
	original, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := original.Seal(secrets.Secret(password))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Replacing config.yml is how this happens in practice.
	other, err := secrets.New(bytes.Repeat([]byte{0x99}, 32))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := other.Open(sealed); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("Open under a different key = %v, want ErrCorrupt", err)
	}
}

func TestCipher_RejectsATruncatedValue(t *testing.T) {
	cipher, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := cipher.Open([]byte{1, 2, 3}); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("Open of a truncated value = %v, want ErrCorrupt", err)
	}
}

func TestNew_RejectsAWrongSizedKeyWithoutEchoingIt(t *testing.T) {
	_, err := secrets.New([]byte("S3CR3T-but-too-short"))
	if err == nil {
		t.Fatal("a short key was accepted")
	}
	if strings.Contains(err.Error(), "S3CR3T") {
		t.Errorf("New echoed the key: %v", err)
	}
}

func TestCipher_HandlesAnEmptySecret(t *testing.T) {
	cipher, err := secrets.New(testKey())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sealed, err := cipher.Seal("")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := cipher.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !opened.IsZero() {
		t.Errorf("round trip of an empty secret produced %q", opened.Reveal())
	}
}
