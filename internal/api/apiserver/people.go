package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
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
	// PresentCount and RequiredCount are the album's own "9 of 12".
	PresentCount  *int64
	RequiredCount *int64
	// AvailableFrom is the release date, nil when unknown; Downloading
	// reports an acquisition in flight. Both feed AlbumStatus.
	AvailableFrom *time.Time
	Downloading   bool
	// Unverified is an album known only by its own tags or folder (D6).
	Unverified bool
}

// AlbumStatus is the colour an album carries wherever it is listed: red
// incomplete, purple downloading, yellow not released yet, green complete
// (2026-09-19). One vocabulary, so the artist page, the poster wall and the
// album page cannot drift apart.
type AlbumStatus string

// The four states, in the order albumStatus decides them.
const (
	AlbumUnreleased  AlbumStatus = "unreleased"
	AlbumUnverified  AlbumStatus = "unverified"
	AlbumDownloading AlbumStatus = "downloading"
	AlbumComplete    AlbumStatus = "complete"
	AlbumIncomplete  AlbumStatus = "incomplete"
)

// albumStatus decides one album's colour. Unreleased outranks everything:
// an album that is not out yet is not missing, and colouring it red would
// report a problem nobody can act on. Downloading outranks complete so a
// finishing upgrade is visible while it runs.
func albumStatus(row PersonWorkRow, now time.Time) AlbumStatus {
	switch {
	case row.Unverified:
		return AlbumUnverified
	case row.AvailableFrom != nil && row.AvailableFrom.After(now):
		return AlbumUnreleased
	case row.Downloading:
		return AlbumDownloading
	case row.WantedState != nil && *row.WantedState == "COMPLETE":
		return AlbumComplete
	case row.WantedState == nil && row.OnDisk:
		// Nothing wants it, but the files are here: on disk is complete.
		return AlbumComplete
	default:
		return AlbumIncomplete
	}
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
	// Subscribe answers a missing person id with an error wrapping
	// sql.ErrNoRows. Both type lists are nil for "use the default" -- the
	// library's own for primary types, the media type's for secondary ones
	// -- and non-nil (however short, including empty) for an explicit
	// per-artist override, which todo 4.4's filter preview needs.
	Subscribe(
		ctx context.Context, personID, userID, categoryID int64, mediaTypes, roles []string,
		monitorBackCatalogue bool, includePrimaryTypes, excludeSecondaryTypes *[]string,
	) error
	// Remove unsubscribes, unmonitors and deletes the person. It never
	// touches files: removing an artist means no longer following them.
	// A missing person answers an error wrapping sql.ErrNoRows.
	Remove(ctx context.Context, personID, userID int64) error
}

// PreviewReleaseGroupRow is one release group in an artist's discography,
// unfiltered -- what the subscribe screen lets a user weigh a filter choice
// against before saving (todo 4.4).
type PreviewReleaseGroupRow struct {
	MBID             string
	Title            string
	PrimaryType      string
	FirstReleaseDate string
	SecondaryTypes   []string
}

// SubscriptionPreviewRow is an artist's full discography plus the default
// exclusion list, so the client can pre-check the same boxes a subscription
// with no filter override would get.
type SubscriptionPreviewRow struct {
	ReleaseGroups                 []PreviewReleaseGroupRow
	DefaultExcludedSecondaryTypes []string
}

// SubscriptionPreviewer answers what an artist's discography looks like
// before a subscription filter is saved (ADR-38's preview-before-enable
// shape, todo 4.4).
type SubscriptionPreviewer interface {
	// Preview answers a missing person id with an error wrapping
	// sql.ErrNoRows, and a person with no MusicBrainz artist id yet with
	// ErrNoArtistID.
	Preview(ctx context.Context, personID int64) (SubscriptionPreviewRow, error)
}

// ErrNoArtistID reports a person with no MusicBrainz artist external id yet
// -- there is no discography to browse until one exists (the same gap
// SubscriptionExpandJob's own expandOne already reports, mapped to 409 here
// since it's a real, recoverable precondition rather than a missing person).
var ErrNoArtistID = errors.New("apiserver: person has no musicbrainz artist id yet")

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
	writeJSON(w, s.logger, http.StatusOK, buildPersonDetailView(detail, s.wall.Now()))
}

func buildPersonDetailView(detail PersonDetailRow, now time.Time) gen.PersonDetail {
	discography := make([]gen.PersonWork, len(detail.Discography))
	for index, row := range detail.Discography {
		discography[index] = gen.PersonWork{
			Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
			Year: yearOf(row.Year), PrimaryType: row.PrimaryType, OnDisk: row.OnDisk,
			PresentCount: row.PresentCount, RequiredCount: row.RequiredCount,
			Status: gen.PersonWorkStatus(albumStatus(row, now)),
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

	// The artist's albums become wanted in the first library of the media type the caller may
	// request in; none is 403, as searching there would be (2026-09-28).
	mediaType := "music"
	for _, named := range body.MediaTypes {
		if named != "" {
			mediaType = named
			break
		}
	}
	var categoryID int64
	for _, category := range access.Categories(settings.PermissionRequest) {
		if category.MediaType == mediaType {
			categoryID = category.ID
			break
		}
	}
	if categoryID == 0 {
		writeError(w, s.logger, fmt.Errorf("%w: no %s library allows requests", authz.ErrInsufficientPermission, mediaType))
		return
	}

	roles := derefOr(body.Roles, nil)
	err = s.peopleSubscriber.Subscribe(r.Context(), id, access.UserID(), categoryID,
		body.MediaTypes, roles, derefOr(body.MonitorBackCatalogue, false),
		body.IncludePrimaryTypes, body.ExcludeSecondaryTypes)
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

// routePreviewSubscription parses the id path parameter, mirroring routeGetWork.
func (s *Server) routePreviewSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a person id must be a number")
		return
	}
	s.PreviewSubscription(w, r, id)
}

// PreviewSubscription answers an artist's full discography, unfiltered, so
// the subscribe screen can show what a filter choice would admit or exclude
// before saving (todo 4.4, ADR-38's preview-before-enable shape).
func (s *Server) PreviewSubscription(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.subscriptionPreviewer == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "subscription preview is not available")
		return
	}

	preview, err := s.subscriptionPreviewer.Preview(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	if errors.Is(err, ErrNoArtistID) {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict,
			"No artist id yet", "this person has no MusicBrainz artist id to browse a discography for")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildSubscriptionPreviewView(preview))
}

func buildSubscriptionPreviewView(row SubscriptionPreviewRow) gen.SubscriptionPreview {
	groups := make([]gen.PreviewReleaseGroup, len(row.ReleaseGroups))
	for index, group := range row.ReleaseGroups {
		groups[index] = gen.PreviewReleaseGroup{
			Mbid: group.MBID, Title: group.Title, SecondaryTypes: group.SecondaryTypes,
		}
		if group.PrimaryType != "" {
			primaryType := group.PrimaryType
			groups[index].PrimaryType = &primaryType
		}
		if group.FirstReleaseDate != "" {
			date := group.FirstReleaseDate
			groups[index].FirstReleaseDate = &date
		}
	}
	return gen.SubscriptionPreview{
		ReleaseGroups: groups, DefaultExcludedSecondaryTypes: row.DefaultExcludedSecondaryTypes,
	}
}

func (s *Server) routeRemovePerson(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a person id must be a number")
		return
	}
	s.RemovePerson(w, r, id)
}

// RemovePerson stops following an artist and forgets them (revision 7.3).
// Administrator only, and deliberately not a DELETE on /people/{id}: the
// files stay, so this removes a relationship rather than the content.
func (s *Server) RemovePerson(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.peopleSubscriber == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "people are not available")
		return
	}
	if err := s.peopleSubscriber.Remove(r.Context(), id, access.UserID()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
