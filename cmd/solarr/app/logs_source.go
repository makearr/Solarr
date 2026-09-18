package app

import (
	"log/slog"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/platform/logbuffer"
)

// logSource satisfies apiserver.LogReader over the process's own log ring.
// Here rather than in adapters for the same reason backupSource is: it needs
// a value the process owns, not a database.
type logSource struct {
	ring *logbuffer.Ring
}

func (s logSource) Records(level, query string, limit int) []apiserver.LogRecordRow {
	if s.ring == nil {
		return nil
	}
	filter := logbuffer.Filter{Contains: query, Limit: limit}
	// An unparseable level means "everything", matching the endpoint's own
	// default rather than silently hiding records.
	if level != "" {
		var parsed slog.Level
		if err := parsed.UnmarshalText([]byte(level)); err == nil {
			filter.MinLevel = parsed
		}
	}
	records := s.ring.Records(filter)
	rows := make([]apiserver.LogRecordRow, len(records))
	for i, record := range records {
		rows[i] = apiserver.LogRecordRow{
			Time: record.Time, Level: record.Level, Message: record.Message, Attrs: record.Attrs,
		}
	}
	return rows
}
