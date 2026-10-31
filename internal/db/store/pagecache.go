package store

import (
	"context"
	"database/sql"
	"time"
)

// pageCacheWait is how long a report waits for a busy connection before leaving it out; the report must never queue behind work.
const pageCacheWait = 200 * time.Millisecond

// PageCache is what one database's SQLite page cache holds and how often it answered, summed over the connections the
// report could reach. SQLite counts hits itself, so reading them costs the query path nothing.
type PageCache struct {
	Name string
	// FileBytes and WALBytes are the sizes on disk.
	FileBytes, WALBytes int64
	// PerConnectionKB is the limit each connection's cache is held to.
	PerConnectionKB int
	// Connections is how many connections were sampled; LimitBytes and UsedBytes cover those only.
	Connections           int
	LimitBytes, UsedBytes int64
	// Hits and Misses are cumulative since the connections opened; both are zero when the driver cannot count.
	Hits, Misses int64
}

// PageCache reports the database's page cache and file sizes.
func (s *Store) PageCache(ctx context.Context) PageCache {
	report := PageCache{Name: s.config.Name, PerConnectionKB: s.config.CacheSizeKB}
	if info, err := s.fs.Stat(s.config.Path); err == nil {
		report.FileBytes = info.Size
	}
	if info, err := s.fs.Stat(s.config.Path + "-wal"); err == nil {
		report.WALBytes = info.Size
	}

	waited, cancel := context.WithTimeout(ctx, pageCacheWait)
	defer cancel()
	var held []*sql.Conn
	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
	}()
	for _, pool := range []*sql.DB{s.write, s.read} {
		limit := 1
		if pool == s.read {
			limit = s.config.ReadPoolSize
		}
		for range limit {
			conn, err := pool.Conn(waited)
			if err != nil {
				break
			}
			held = append(held, conn)
			used, hits, misses, counted := pageCacheCounters(waited, conn)
			if !counted {
				continue
			}
			report.Connections++
			report.UsedBytes += used
			report.Hits += hits
			report.Misses += misses
		}
	}
	report.LimitBytes = int64(report.Connections) * int64(s.config.CacheSizeKB) * 1024
	return report
}
