package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/platform/paginate"
)

// PersonRow is one person as the library reports it.
type PersonRow struct {
	ID             int64
	Name           string
	SortName       string
	Disambiguation *string
	Kind           *string
	Gender         *string
	Country        *string
	Born           *string
	Died           *string
	Monitored      bool
}

// PersonFilter narrows a ListPeople call. A zero field is not a filter.
type PersonFilter struct {
	Monitored  *bool
	NamePrefix string
}

// PersonCursor is a position in the people list.
type PersonCursor struct {
	SortName string
	ID       int64
}

// PersonWorkRow is one work in a person's discography, plus the artist
// page's own "available / missing / requested" signal (Lidarr-like
// discography, user request 2026-09-12).
type PersonWorkRow struct {
	ID          int64
	MediaType   string
	Title       string
	SortTitle   string
	Year        *int64
	PrimaryType *string
	// WantedState is nil when nothing has ever wanted this work -- Work's
	// own wanted_state doc comment.
	WantedState *string
	OnDisk      bool
}

// PersonDetailRow is a person's full detail. Coverage (held vs missing vs
// upgradable) is not included -- see the OpenAPI description for why.
type PersonDetailRow struct {
	PersonRow
	Biography *string
	Aliases   []string
	// Genres is the union of every genre tagged on any work in Discography
	// -- Person carries no genre of its own (ADR-32's fields are biographical).
	Genres      []string
	Discography []PersonWorkRow
}

// PeopleReader lists and reads people (ADR-32). Defined here and implemented
// by the caller, same reasoning as WorksReader's own doc comment.
type PeopleReader interface {
	ListPeople(ctx context.Context, filter PersonFilter, cursor PersonCursor, pageSize int) ([]PersonRow, error)
	// GetPersonDetail answers a missing id with an error wrapping sql.ErrNoRows.
	GetPersonDetail(ctx context.Context, id int64) (PersonDetailRow, error)
}

// PeopleSubscriber subscribes the calling user to a person (ADR-32).
type PeopleSubscriber interface {
	// Subscribe answers a missing person id with an error wrapping sql.ErrNoRows.
	Subscribe(ctx context.Context, personID, userID int64, mediaTypes, roles []string, monitorBackCatalogue bool) error
}

// routeListPeople parses the query string, mirroring routeListWorks.
func (s *Server) routeListPeople(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListPeopleParams

	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	if raw := query.Get("monitored"); raw != "" {
		monitored, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "monitored must be true or false")
			return
		}
		params.Monitored = &monitored
	}
	if raw := query.Get("q"); raw != "" {
		params.Q = &raw
	}

	s.ListPeople(w, r, params)
}

// ListPeople answers a page of people. Unlike works, not category-scoped: a
// person legitimately spans several media types and categories at once
// (ADR-32), so there is no single visibility boundary to apply the way
// ADR-27 applies to one work.
func (s *Server) ListPeople(w http.ResponseWriter, r *http.Request, params gen.ListPeopleParams) {
	if s.people == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "people are not available")
		return
	}

	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))

	rows, err := s.people.ListPeople(r.Context(),
		PersonFilter{Monitored: params.Monitored, NamePrefix: derefOr(params.Q, "")},
		PersonCursor{SortName: cursor.Sort, ID: cursor.ID}, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildPersonPage(rows, limit))
}

func buildPersonPage(rows []PersonRow, limit int) gen.PersonPage {
	items := rows
	var nextCursor string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		nextCursor = paginate.TextCursor{Sort: last.SortName, ID: last.ID}.Encode()
	}

	views := make([]gen.Person, len(items))
	for index, row := range items {
		views[index] = buildPersonView(row)
	}
	page := gen.PersonPage{Items: views}
	if nextCursor != "" {
		page.NextCursor = &nextCursor
	}
	return page
}

func buildPersonView(row PersonRow) gen.Person {
	return gen.Person{
		Id: row.ID, Name: row.Name, SortName: row.SortName,
		Disambiguation: row.Disambiguation, Kind: row.Kind, Gender: row.Gender,
		Country: row.Country, Born: row.Born, Died: row.Died, Monitored: row.Monitored,
	}
}

// routeGetPerson parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeGetPerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a person id must be a number")
		return
	}
	s.GetPerson(w, r, id)
}

// GetPerson answers one person's full detail.
func (s *Server) GetPerson(w http.ResponseWriter, r *http.Request, id int64) {
	if s.people == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "people are not available")
		return
	}

	detail, err := s.people.GetPersonDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildPersonDetailView(detail))
}

func buildPersonDetailView(detail PersonDetailRow) gen.PersonDetail {
	discography := make([]gen.PersonWork, len(detail.Discography))
	for index, row := range detail.Discography {
		discography[index] = gen.PersonWork{
			Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
			Year: yearOf(row.Year), PrimaryType: row.PrimaryType, OnDisk: row.OnDisk,
		}
		if row.WantedState != nil {
			state := gen.PersonWorkWantedState(*row.WantedState)
			discography[index].WantedState = &state
		}
	}
	return gen.PersonDetail{
		Id: detail.ID, Name: detail.Name, SortName: detail.SortName,
		Disambiguation: detail.Disambiguation, Kind: detail.Kind, Gender: detail.Gender,
		Country: detail.Country, Born: detail.Born, Died: detail.Died, Monitored: detail.Monitored,
		Biography: detail.Biography, Aliases: detail.Aliases, Genres: detail.Genres, Discography: discography,
	}
}

// routeSubscribePerson parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeSubscribePerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a person id must be a number")
		return
	}
	s.SubscribePerson(w, r, id)
}

// SubscribePerson subscribes the calling user to a person (ADR-32) and marks
// them monitored. The request body is decoded here, not by a generated
// wrapper (ADR-31: chi-server generates the interface only, routing and body
// decoding stay hand-written, same as Login/ImportSettings).
func (s *Server) SubscribePerson(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body gen.SubscribePersonJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}
	if len(body.MediaTypes) == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "media_types must name at least one media type")
		return
	}
	if s.peopleSubscriber == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "subscriptions are not available")
		return
	}

	roles := derefOr(body.Roles, nil)
	err = s.peopleSubscriber.Subscribe(r.Context(), id, access.UserID(),
		body.MediaTypes, roles, derefOr(body.MonitorBackCatalogue, false))
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
