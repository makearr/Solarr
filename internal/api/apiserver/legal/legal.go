// Package legal embeds the licence and attributions AGPL section 13 requires a deployed binary to offer (documentation/licence-compliance.md).
package legal

import _ "embed"

// License is byte-for-byte the repository LICENSE; `make license-check` fails if it drifts.
//
//go:embed LICENSE
var License string

// Attribution is byte-for-byte the repository's own ATTRIBUTION.md.
//
//go:embed ATTRIBUTION.md
var Attribution string
