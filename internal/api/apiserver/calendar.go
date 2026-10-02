package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// CalendarRow is one release in the window.
type CalendarRow struct {
	WorkID      int64
	Title       string
	Artist      string
	PrimaryType string
	ReleaseDate string
	OnDisk      bool
	// PartID and CategoryID are set for a TV episode.
	PartID, CategoryID int64
}

// CalendarReader answers releases in a date window. Defined here and
// implemented by the caller, the same separation WorksReader keeps.
type CalendarReader interface {
	// Entries answers only works wanted by one of categoryIDs, the libraries the caller can see.
	Entries(ctx context.Context, from, to string, categoryIDs []int64, limit int) ([]CalendarRow, error)
	// Dates counts the same releases per date.
	Dates(ctx context.Context, from, to string, categoryIDs []int64) ([]CalendarDate, error)
}

// CalendarDate is how many releases share one date.
type CalendarDate struct {
	Date     string
	Releases int64
}

// isoDate is the shortest and longest a date parameter may be: a bare year up
// to a full day. Validated rather than parsed, because a partial date is a
// real answer here and time.Parse would refuse it.
func isoDate(value string) bool {
	if len(value) != 4 && len(value) != 7 && len(value) != 10 {
		return false
	}
	for index, r := range value {
		switch {
		case index == 4 || index == 7:
			if r != '-' {
				return false
			}
		case r < '0' || r > '9':
			return false
		}
	}
	return true
}

func (s *Server) routeListCalendar(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, to := query.Get("from"), query.Get("to")
	if !isoDate(from) || !isoDate(to) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "from and to must be ISO dates: 2024, 2024-03 or 2024-03-01")
		return
	}
	params := gen.ListCalendarParams{From: from, To: to}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListCalendar(w, r, params)
}

// ListCalendar answers releases in a date window, earliest first.
func (s *Server) ListCalendar(w http.ResponseWriter, r *http.Request, params gen.ListCalendarParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.calendar == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the calendar is not available")
		return
	}

	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rows, err := s.calendar.Entries(r.Context(), params.From, params.To,
		access.VisibleCategoryIDs(settings.PermissionRead), limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.CalendarEntry, len(rows))
	for i, row := range rows {
		view := gen.CalendarEntry{
			WorkId: row.WorkID, Title: row.Title, ReleaseDate: row.ReleaseDate, OnDisk: row.OnDisk,
		}
		if row.Artist != "" {
			view.Artist = &row.Artist
		}
		if row.PrimaryType != "" {
			view.PrimaryType = &row.PrimaryType
		}
		if row.PartID != 0 {
			view.PartId, view.CategoryId = &row.PartID, &row.CategoryID
		}
		views[i] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) routeListCalendarDates(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	from, to := query.Get("from"), query.Get("to")
	if !isoDate(from) || !isoDate(to) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "from and to must be ISO dates: 2024, 2024-03 or 2024-03-01")
		return
	}
	s.ListCalendarDates(w, r, gen.ListCalendarDatesParams{From: from, To: to})
}

// ListCalendarDates counts releases per date, only in libraries the caller can see.
func (s *Server) ListCalendarDates(w http.ResponseWriter, r *http.Request, params gen.ListCalendarDatesParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.calendar == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the calendar is not available")
		return
	}
	dates, err := s.calendar.Dates(r.Context(), params.From, params.To, access.VisibleCategoryIDs(settings.PermissionRead))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.CalendarDate, len(dates))
	for i, date := range dates {
		views[i] = gen.CalendarDate{Date: date.Date, Releases: date.Releases}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}
