// Package secrets encrypts credentials at rest and keeps them out of everything that is not the
// database (ADR-14, ADR-25, Invariant 40).
package secrets

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// redacted is what a secret renders as everywhere except the database.
const redacted = "<redacted>"

// Secret is a credential (Invariant 61).
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

// Mask renders a secret for display.
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
