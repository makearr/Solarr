//go:build ncruces

package store

import (
	_ "github.com/ncruces/go-sqlite3/driver" // the SQLite C library under wazero, still CGO-free
)

// driverName is the only place the driver is named (ADR-5).
const driverName = "sqlite3"
