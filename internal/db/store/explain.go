package store

import (
	"context"
	"fmt"
	"strings"
)

// ExplainQueryPlan returns SQLite's plan for query, one line per step indented two spaces per
// nesting level, so a test can tell the outer ORDER BY from a correlated subquery's. EXPLAIN never checks the schema cookie, so a pooled connection
// that loaded the schema mid-migration would explain against old indexes: read the schema first.
func (s *Store) ExplainQueryPlan(ctx context.Context, query string, arguments ...any) (string, error) {
	conn, err := s.read.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("store %s: explain: %w", s.config.Name, err)
	}
	defer func() { _ = conn.Close() }()
	var objects int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
		return "", fmt.Errorf("store %s: explain: load schema: %w", s.config.Name, err)
	}

	rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, arguments...)
	if err != nil {
		return "", fmt.Errorf("store %s: explain: %w", s.config.Name, err)
	}
	defer func() { _ = rows.Close() }()
	var plan strings.Builder
	depth := map[int]int{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			return "", fmt.Errorf("store %s: scan plan: %w", s.config.Name, err)
		}
		if parent != 0 {
			depth[id] = depth[parent] + 1
		}
		plan.WriteString(strings.Repeat("  ", depth[id]) + detail + "\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("store %s: explain: %w", s.config.Name, err)
	}
	return plan.String(), nil
}
