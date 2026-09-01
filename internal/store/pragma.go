package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// pragmaExpectation is one setting and the value every connection must report.
// The DSN asks for these; this checks the driver actually applied them, because
// a silently ignored PRAGMA is indistinguishable from a working one until the
// day it matters (Invariant 4).
type pragmaExpectation struct {
	name string
	want string
}

func (s *Store) expectations() []pragmaExpectation {
	return []pragmaExpectation{
		{"journal_mode", "wal"},
		{"synchronous", "1"}, // NORMAL
		{"foreign_keys", "1"},
		{"temp_store", "2"}, // MEMORY
		{"busy_timeout", fmt.Sprint(s.config.BusyTimeout.Milliseconds())},
		{"page_size", fmt.Sprint(s.config.PageSize)},
		{"cache_size", fmt.Sprint(-s.config.CacheSizeKB)},
	}
}

// verifyPragmas checks the write connection and every connection in the read
// pool. Checking one read connection would prove nothing: the failure this
// guards against is precisely that some connections have the settings and
// others do not.
func (s *Store) verifyPragmas(ctx context.Context) error {
	if err := verifyOn(ctx, s.write, s.expectations(), s.config.Name, "write"); err != nil {
		return err
	}

	// Pin every connection at once, so each is checked individually rather than
	// one being checked repeatedly.
	pinned := make([]*sql.Conn, 0, s.config.ReadPoolSize)
	defer func() {
		for _, conn := range pinned {
			_ = conn.Close()
		}
	}()

	for index := range s.config.ReadPoolSize {
		conn, err := s.read.Conn(ctx)
		if err != nil {
			return fmt.Errorf("store %s: pin read connection %d: %w", s.config.Name, index, err)
		}
		pinned = append(pinned, conn)
		if err := verifyConn(ctx, conn, s.expectations(), s.config.Name, fmt.Sprintf("read[%d]", index)); err != nil {
			return err
		}
	}
	return nil
}

func verifyOn(ctx context.Context, db *sql.DB, expectations []pragmaExpectation, database, role string) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store %s: pin %s connection: %w", database, role, err)
	}
	defer func() { _ = conn.Close() }()
	return verifyConn(ctx, conn, expectations, database, role)
}

func verifyConn(ctx context.Context, conn *sql.Conn, expectations []pragmaExpectation, database, role string) error {
	for _, expectation := range expectations {
		var got string
		// The PRAGMA name comes from the table above, never from input.
		query := "PRAGMA " + expectation.name
		if err := conn.QueryRowContext(ctx, query).Scan(&got); err != nil {
			return fmt.Errorf("store %s: read %s on the %s connection: %w", database, expectation.name, role, err)
		}
		if !strings.EqualFold(got, expectation.want) {
			return fmt.Errorf("store %s: %s connection has %s=%s, want %s — the DSN did not apply (Invariant 4)",
				database, role, expectation.name, got, expectation.want)
		}
	}
	return nil
}
