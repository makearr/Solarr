//go:build ncruces

package store

import (
	"context"
	"database/sql"

	_ "github.com/ncruces/go-sqlite3/driver" // the SQLite C library under wazero, still CGO-free
)

// driverName is the only place the driver is named (ADR-5).
const driverName = "sqlite3"

// DriverName is what this binary was built with, which config.yml does not decide.
func DriverName() string { return "ncruces" }

// pageCacheCounters is not available through this driver's database/sql wrapper.
func pageCacheCounters(context.Context, *sql.Conn) (used, hits, misses int64, counted bool) {
	return 0, 0, 0, false
}
