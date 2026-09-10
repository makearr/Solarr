package store

import (
	"fmt"
	"net/url"
	"strings"
)

// buildDSN puts every PRAGMA in the connection string (ADR-2, Invariant 4). The cache
// is per connection and passed in, because the checkpointer needs far less of one than
// a reader does.
func buildDSN(config Config, cacheSizeKB int, immediateTransactions bool) string {
	pragmas := []string{
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
		fmt.Sprintf("busy_timeout(%d)", config.BusyTimeout.Milliseconds()),
		"temp_store(MEMORY)",
		fmt.Sprintf("cache_size(-%d)", cacheSizeKB),
		// Off, because SQLite checkpoints inside whichever transaction crosses the
		// threshold: checkpointLoop does it on its own schedule instead.
		"wal_autocheckpoint(0)",
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
