package apiserver

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// GetMyCalendarFeed answers the caller's feed token, so the link can be copied again.
func (s *Server) GetMyCalendarFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.feedOwner(w, r)
	if !ok {
		return
	}
	token, found, err := s.auth.Settings.CalendarFeed(r.Context(), userID)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.CalendarFeed{}
	if found {
		revealed := token.Reveal()
		view.Token = &revealed
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// CreateMyCalendarFeed gives the caller a new token; the previous link stops working.
func (s *Server) CreateMyCalendarFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.feedOwner(w, r)
	if !ok {
		return
	}
	token, hash, err := auth.GenerateCalendarToken()
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.SetCalendarFeed(r.Context(), s.wall, userID, token, hash); err != nil {
		writeError(w, s.logger, err)
		return
	}
	revealed := token.Reveal()
	writeJSON(w, s.logger, http.StatusOK, gen.CalendarFeed{Token: &revealed})
}

// DeleteMyCalendarFeed revokes the caller's feed.
func (s *Server) DeleteMyCalendarFeed(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.feedOwner(w, r)
	if !ok {
		return
	}
	if err := s.auth.Settings.DeleteCalendarFeed(r.Context(), userID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// feedOwner is the signed-in user; an instance that needs no login has no user to own a feed.
func (s *Server) feedOwner(w http.ResponseWriter, r *http.Request) (int64, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return 0, false
	}
	if access.IsSystem() {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "No user account",
			"a calendar feed belongs to a user account, and this request has none")
		return 0, false
	}
	return access.UserID(), true
}

// routeCalendarFeedICS is the one calendar route with no session: the token is the credential.
func (s *Server) routeCalendarFeedICS(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	params := gen.GetCalendarFeedICSParams{Token: query.Get("token")}
	for name, target := range map[string]**int{"past_days": &params.PastDays, "future_days": &params.FutureDays} {
		if raw := query.Get(name); raw != "" {
			days, err := strconv.Atoi(raw)
			if err != nil || days < 0 || days > 3650 {
				writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
					"Bad request", name+" must be a number of days from 0 to 3650")
				return
			}
			*target = &days
		}
	}
	s.GetCalendarFeedICS(w, r, params)
}

// GetCalendarFeedICS renders the token's user's calendar, their libraries only.
func (s *Server) GetCalendarFeedICS(w http.ResponseWriter, r *http.Request, params gen.GetCalendarFeedICSParams) {
	// An unknown or revoked token answers the same 404 as a malformed one: nothing to probe.
	notFound := func() {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such calendar feed")
	}
	if !auth.LooksLikeCalendarToken(params.Token) {
		notFound()
		return
	}
	user, err := s.auth.Settings.UserByCalendarFeedHash(r.Context(), auth.HashAPIKey(params.Token))
	if errors.Is(err, settings.ErrNotFound) {
		notFound()
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.calendar == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the calendar is not available")
		return
	}

	now := s.wall.Now().UTC()
	from := now.AddDate(0, 0, -derefOr(params.PastDays, 30)).Format(time.DateOnly)
	to := now.AddDate(0, 0, derefOr(params.FutureDays, 365)).Format(time.DateOnly)
	// The token stands in for a session: the feed sees what its user would see signed in.
	access := authz.ForUser(s.auth.Settings.Snapshot(), user)
	categories := access.VisibleCategoryIDs(settings.PermissionRead)
	rows, err := s.calendar.Entries(r.Context(), from, to, categories, calendarFeedLimit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="solarr.ics"`)
	_, _ = w.Write([]byte(renderICS(rows, now)))
}

// calendarFeedLimit is the most releases one feed carries; a year of a large library fits.
const calendarFeedLimit = 500

// renderICS writes one all-day event per release with a full date (RFC 5545). A release dated
// only by month or year has no day to put it on, so a calendar client never sees it.
func renderICS(rows []CalendarRow, now time.Time) string {
	var b strings.Builder
	line := func(text string) {
		b.WriteString(foldICS(text))
		b.WriteString("\r\n")
	}
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Solarr//Calendar//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Solarr")
	stamp := now.Format("20060102T150405Z")
	for _, row := range rows {
		day, err := time.Parse(time.DateOnly, row.ReleaseDate)
		if err != nil {
			continue
		}
		summary := row.Title
		if row.Artist != "" {
			summary = row.Artist + " - " + row.Title
		}
		status := "Wanted"
		if row.OnDisk {
			status = "In the library"
		}
		if row.PrimaryType != "" {
			status = row.PrimaryType + ", " + strings.ToLower(status[:1]) + status[1:]
		}
		line("BEGIN:VEVENT")
		// An episode is its own event: a series' episodes share the work id, and a calendar merges equal UIDs.
		if row.PartID != 0 {
			line(fmt.Sprintf("UID:solarr-work-%d-part-%d", row.WorkID, row.PartID))
		} else {
			line(fmt.Sprintf("UID:solarr-work-%d", row.WorkID))
		}
		line("DTSTAMP:" + stamp)
		line("DTSTART;VALUE=DATE:" + day.Format("20060102"))
		line("DTEND;VALUE=DATE:" + day.AddDate(0, 0, 1).Format("20060102"))
		line("SUMMARY:" + escapeICS(summary))
		line("DESCRIPTION:" + escapeICS(status))
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.String()
}

var icsEscaper = strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`, "\r", "")

func escapeICS(text string) string { return icsEscaper.Replace(text) }

// foldICS splits a content line at 75 octets, never inside a UTF-8 sequence (RFC 5545 3.1).
func foldICS(text string) string {
	if len(text) <= 75 {
		return text
	}
	var b strings.Builder
	limit := 75
	for len(text) > limit {
		cut := limit
		for cut > 0 && text[cut]&0xC0 == 0x80 {
			cut--
		}
		b.WriteString(text[:cut])
		b.WriteString("\r\n ")
		text = text[cut:]
		limit = 74 // the continuation's leading space counts toward its 75
	}
	b.WriteString(text)
	return b.String()
}
