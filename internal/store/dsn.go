package store

import (
	"fmt"
	"net/url"
	"strings"
)

// buildDSN puts every PRAGMA in the connection string.
//
// PRAGMAs are per-connection, not per-database (ADR-2). With database/sql
// pooling, running them once after Open silently applies them to one connection
// and not the rest, so they go in the DSN where the driver replays them on every
// connection it makes. That is Invariant 4, and there is a test that opens the
// whole pool at once and checks each connection individually.
//
// page_size is deliberately absent: it cannot be set on a WAL database at all,
// so ensurePageSize settles it beforehand on its own connection. Leaving it here
// would imply it still did something.
func buildDSN(config Config, immediateTransactions bool) string {
	pragmas := []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()),
		"temp_store(MEMORY)",
		fmt.Sprintf("cache_size(-%d)", config.CacheSizeKB),
	}

	parameters := make([]string, 0, len(pragmas)+1)
	for _, pragma := range pragmas {
		parameters = append(parameters, "_pragma="+url.QueryEscape(pragma))
	}
	if immediateTransactions {
		// BEGIN IMMEDIATE takes the write lock up front, turning a
		// mid-transaction SQLITE_BUSY into a clean wait at the start (§4.1).
		parameters = append(parameters, "_txlock=immediate")
	}

	return "file:" + config.Path + "?" + strings.Join(parameters, "&")
}
