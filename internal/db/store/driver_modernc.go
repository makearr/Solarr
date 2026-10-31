//go:build !ncruces

package store

import (
	"context"
	"database/sql"
	"errors"

	"modernc.org/sqlite" // pure Go, so CGO_ENABLED=0 keeps building (ADR-5, ADR-30)
)

// driverName is the only place the driver is named (ADR-5).
const driverName = "sqlite"

// DriverName is what this binary was built with, which config.yml does not decide.
func DriverName() string { return "modernc" }

// pageCacheCounters reads SQLite's own page cache counters of one connection.
func pageCacheCounters(_ context.Context, conn *sql.Conn) (used, hits, misses int64, counted bool) {
	err := conn.Raw(func(driverConn any) error {
		status, ok := driverConn.(sqlite.DBStatus)
		if !ok {
			return errNoStatus
		}
		for _, counter := range []struct {
			op   sqlite.DBStatusOp
			into *int64
		}{{sqlite.DBStatusCacheUsed, &used}, {sqlite.DBStatusCacheHit, &hits}, {sqlite.DBStatusCacheMiss, &misses}} {
			current, _, err := status.Status(counter.op, false)
			if err != nil {
				return err
			}
			*counter.into = int64(current)
		}
		return nil
	})
	return used, hits, misses, err == nil
}

var errNoStatus = errors.New("store: driver connection does not report cache status")
