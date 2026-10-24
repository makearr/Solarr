package apiserver

import (
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// LogRecordRow is one captured log line.
type LogRecordRow struct {
	Time    time.Time
	Level   string
	Message string
	Attrs   []string
}

// LogReader answers the most recent log records. Defined here and implemented
// by the caller, so this package never imports the log buffer itself.
type LogReader interface {
	// Records answers matching records newest first. An empty level means
	// everything captured; an empty query matches everything.
	Records(level, query string, limit int) []LogRecordRow
}

func (s *Server) routeListLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.ListLogsParams{}
	if raw := query.Get("level"); raw != "" {
		level := gen.ListLogsParamsLevel(raw)
		switch level {
		case gen.ListLogsParamsLevelDebug, gen.ListLogsParamsLevelInfo,
			gen.ListLogsParamsLevelWarn, gen.ListLogsParamsLevelError:
			params.Level = &level
		default:
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "level must be debug, info, warn or error")
			return
		}
	}
	if raw := query.Get("q"); raw != "" {
		params.Q = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListLogs(w, r, params)
}

// ListLogs answers the most recent log records, newest first.
func (s *Server) ListLogs(w http.ResponseWriter, r *http.Request, params gen.ListLogsParams) {
	// The log names accounts, addresses and paths from every library.
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.logReader == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "logs are not available")
		return
	}

	level := ""
	if params.Level != nil {
		level = string(*params.Level)
	}
	limit := derefOr(params.Limit, 0)
	if limit <= 0 {
		limit = defaultLogLimit
	}
	records := s.logReader.Records(level, derefOr(params.Q, ""), limit)

	views := make([]gen.LogRecord, len(records))
	for i, record := range records {
		view := gen.LogRecord{Time: record.Time, Level: record.Level, Message: record.Message}
		if len(record.Attrs) > 0 {
			attrs := record.Attrs
			view.Attrs = &attrs
		}
		views[i] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// defaultLogLimit matches the spec's own default.
const defaultLogLimit = 200
