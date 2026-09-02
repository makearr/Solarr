//go:build !ncruces

package store

import (
	_ "modernc.org/sqlite" // pure Go, so CGO_ENABLED=0 keeps building (ADR-5, ADR-30)
)

// driverName is the only place the driver is named (ADR-5).
const driverName = "sqlite"
