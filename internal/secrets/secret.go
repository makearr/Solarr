// Package secrets encrypts credentials at rest and keeps them out of everything
// that is not the database (ADR-14).
//
// Credentials live in settings.db and never in library.db (ADR-25,
// Invariant 40). Separation is the primary control and encryption is the
// second: library.db is the file that gets shared, exported and attached to a
// bug report, and it holds no credential in any form.
package secrets

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// redacted is what a secret renders as everywhere except the database.
const redacted = "<redacted>"

// Secret is a credential. Its value cannot be printed, formatted, logged or
// serialised by accident: every path that would normally reveal a string
// returns a placeholder, and reading the real value takes an explicit call.
//
// Invariant 61 says secrets never appear in logs or errors. A convention would
// be forgotten; a type that cannot be printed will not be.
type Secret string

// String satisfies fmt.Stringer, so %s and %v redact.
func (s Secret) String() string { return redacted }

// GoString satisfies fmt.GoStringer, so %#v redacts too.
func (s Secret) GoString() string { return redacted }

// LogValue satisfies slog.LogValuer, so slog.Any redacts.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalJSON redacts, so a secret cannot reach an API response by being
// embedded in a struct someone forgot to filter.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(redacted) }

// Reveal returns the actual value. Every call site is a place to check that the
// secret is going somewhere it belongs.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether there is no secret at all, without revealing one.
func (s Secret) IsZero() bool { return s == "" }

// Masked is how a secret is shown in the API: enough to recognise which
// credential it is, not enough to use it (§13.1).
type Masked struct {
	HasValue bool   `json:"has_value"`
	Masked   string `json:"masked,omitempty"`
}

// Mask renders a secret for display. Short secrets show nothing but their
// length, because revealing three of six characters is most of a six-character
// secret.
func (s Secret) Mask() Masked {
	if s.IsZero() {
		return Masked{HasValue: false}
	}

	value := string(s)
	const minimumToHint = 12
	if len(value) < minimumToHint {
		return Masked{HasValue: true, Masked: strings.Repeat("•", len(value))}
	}
	return Masked{HasValue: true, Masked: fmt.Sprintf("%s…%s", value[:3], value[len(value)-3:])}
}
