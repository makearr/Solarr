// Package legal embeds the texts AGPL section 13 requires the running
// program to offer -- the full licence and third-party attributions -- so
// they are readable from a deployed binary, which carries none of its own
// source tree (documentation/licence-compliance.md).
package legal

import _ "embed"

// License is byte-for-byte the repository's own LICENSE. `make license-check`
// fails the build if this copy drifts from it (ADR-31's rule for every other
// generated or mirrored artefact).
//
//go:embed LICENSE
var License string

// Attribution is byte-for-byte the repository's own ATTRIBUTION.md.
//
//go:embed ATTRIBUTION.md
var Attribution string
