package apiserver

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// ErrPersonNotFound reports a person no library the caller may read knows of: 404, never 403 (ADR-27).
var ErrPersonNotFound = errors.New("apiserver: person not found")

// PeopleQuery narrows ListPeople: Visible are the libraries the caller may read, which nothing widens.
type PeopleQuery struct {
	Visible   []int64
	Category  int64
	Roles     []string
	Monitored *bool
	Prefix    string
	// Where is the custom filter as the JSON the request carried; malformed answers ErrInvalidChoice.
	Where string
	Sort  string
	Desc  bool
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
	// PosterURL is a provider's poster, for a work with no cover of its own.
	PosterURL string
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

// albumStatus decides one album's colour. Unreleased outranks all but a complete album: one not out yet is not missing,
// and red would report a problem nobody can act on. Downloading outranks complete so a finishing upgrade shows.
func albumStatus(row PersonWorkRow, now time.Time) AlbumStatus {
	// Every track is here; below the cutoff is Cutoff Unmet's business, not missing (as Lidarr).
	allHere := row.WantedState != nil && (*row.WantedState == "COMPLETE" || *row.WantedState == "UPGRADABLE")
	switch {
	case row.Unverified:
		return AlbumUnverified
	case row.AvailableFrom != nil && row.AvailableFrom.After(now) && !allHere:
		return AlbumUnreleased
	case row.Downloading:
		return AlbumDownloading
	case allHere:
		return AlbumComplete
	case row.WantedState == nil && row.OnDisk:
		// Nothing wants it, but the files are here: on disk is complete.
		return AlbumComplete
	default:
		return AlbumIncomplete
	}
}

// PersonCreditRow is one credit of a person, its work as every list shows works.
type PersonCreditRow struct {
	CategoryID int64
	Role       string
	Character  string
	Parts      []string
	Work       PersonWorkRow
}

// PeopleReader lists and reads people of every media type (ADR-32). Defined here and implemented by the caller.
type PeopleReader interface {
	// ListPeople answers one page; the cursor's Sort is the sort value of the last row.
	ListPeople(ctx context.Context, query PeopleQuery, after paginate.TextCursor, limit int) ([]gen.PersonEntry, error)
	// GetPersonDetail answers a person no library in visible knows of with ErrPersonNotFound; credits are visible's only.
	GetPersonDetail(ctx context.Context, id int64, visible []int64) (gen.PersonDetail, []PersonCreditRow, error)
	// KnownFor asks one library's media type what its providers know the person for; ErrPersonNotFound as above.
	KnownFor(ctx context.Context, id, categoryID int64, visible []int64) ([]gen.KnownFor, error)
}

// PersonRemover removes an artist the caller no longer wants (revision 7.3).
type PersonRemover interface {
	// Remove unfollows and deletes the person, never their files. A missing person answers an error wrapping
	// sql.ErrNoRows.
	Remove(ctx context.Context, personID, userID int64) error
}

// routeListPeople parses the query string into the generated parameters.
func (s *Server) routeListPeople(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var params gen.ListPeopleParams
	if raw := query.Get("category"); raw != "" {
		category, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category must be a number")
			return
		}
		params.Category = &category
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	if raw := query.Get("monitored"); raw != "" {
		monitored, err := strconv.ParseBool(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "monitored must be true or false")
			return
		}
		params.Monitored = &monitored
	}
	for name, target := range map[string]**string{"cursor": &params.Cursor, "q": &params.Q, "role": &params.Role, "where": &params.Where, "from": &params.From} {
		if raw := query.Get(name); raw != "" {
			*target = &raw
		}
	}
	if raw := query.Get("sort"); raw != "" {
		sort := gen.ListPeopleParamsSort(raw)
		params.Sort = &sort
	}
	if raw := query.Get("dir"); raw != "" {
		dir := gen.ListPeopleParamsDir(raw)
		params.Dir = &dir
	}
	s.ListPeople(w, r, params)
}

// ListPeople answers a page of people of one library, or of every library the caller may read. A person is listed
// when a library the caller may read credits or follows them, and not otherwise (2026-10-04).
func (s *Server) ListPeople(w http.ResponseWriter, r *http.Request, params gen.ListPeopleParams) {
	if s.people == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "people are not available")
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	visible, ok := s.personVisibility(w, r)
	if !ok {
		return
	}
	query := PeopleQuery{
		Visible: visible, Category: derefOr(params.Category, 0), Monitored: params.Monitored, Prefix: derefOr(params.Q, ""),
		Where: derefOr(params.Where, ""), Sort: string(derefOr(params.Sort, gen.ListPeopleParamsSortName)),
		Desc: derefOr(params.Dir, gen.ListPeopleParamsDirAsc) == gen.ListPeopleParamsDirDesc,
	}
	for _, role := range strings.Split(derefOr(params.Role, ""), ",") {
		if role = strings.TrimSpace(role); role != "" {
			query.Roles = append(query.Roles, role)
		}
	}
	if query.Category != 0 && !slices.Contains(visible, query.Category) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such library")
		return
	}
	if cursor.Sort == "" && cursor.ID == 0 && params.From != nil && !query.Desc && query.Sort == string(gen.ListPeopleParamsSortName) {
		cursor = paginate.TextCursor{Sort: *params.From}
	}
	rows, err := s.people.ListPeople(r.Context(), query, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.PersonPage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[limit-1]
		next := paginate.TextCursor{Sort: personSortValue(last, query.Sort), ID: last.Id}.Encode()
		page.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// personSortValue is the value a page of people is ordered by, as its cursor carries it.
func personSortValue(entry gen.PersonEntry, sort string) string {
	switch sort {
	case string(gen.ListPeopleParamsSortWorks):
		return strconv.FormatInt(entry.Works, 10)
	case string(gen.ListPeopleParamsSortLatest):
		return strconv.Itoa(derefOr(entry.Latest, 0))
	case string(gen.ListPeopleParamsSortAdded):
		return strconv.FormatInt(derefOr(entry.AddedAt, 0), 10)
	default:
		return entry.SortName
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

// GetPerson answers one person with their credits in every library the caller may read.
func (s *Server) GetPerson(w http.ResponseWriter, r *http.Request, id int64) {
	if s.people == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "people are not available")
		return
	}
	visible, ok := s.personVisibility(w, r)
	if !ok {
		return
	}
	detail, credits, err := s.people.GetPersonDetail(r.Context(), id, visible)
	if errors.Is(err, ErrPersonNotFound) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	detail.Credits = creditViews(credits, s.wall.Now())
	writeJSON(w, s.logger, http.StatusOK, detail)
}

// ListPersonKnownFor answers what one library's providers know a person for.
func (s *Server) ListPersonKnownFor(w http.ResponseWriter, r *http.Request, id int64, params gen.ListPersonKnownForParams) {
	if s.people == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "people are not available")
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(params.Category, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	visible, ok := s.personVisibility(w, r)
	if !ok {
		return
	}
	titles, err := s.people.KnownFor(r.Context(), id, params.Category, visible)
	if errors.Is(err, ErrPersonNotFound) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
		return
	}
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "Provider unavailable", "the providers could not be asked")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, titles)
}

func (s *Server) routeListPersonKnownFor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a person id must be a number")
		return
	}
	category, err := strconv.ParseInt(r.URL.Query().Get("category"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category must be a number")
		return
	}
	s.ListPersonKnownFor(w, r, id, gen.ListPersonKnownForParams{Category: category})
}

// creditViews maps credits with each work's status colour as of now.
func creditViews(credits []PersonCreditRow, now time.Time) []gen.WorkCredit {
	views := make([]gen.WorkCredit, len(credits))
	for index, credit := range credits {
		view := gen.WorkCredit{CategoryId: credit.CategoryID, Role: credit.Role, Work: personWorkView(credit.Work, now)}
		if credit.Character != "" {
			view.Character = &credit.Character
		}
		if len(credit.Parts) > 0 {
			view.Parts = &credit.Parts
		}
		views[index] = view
	}
	return views
}

func personWorkView(row PersonWorkRow, now time.Time) gen.PersonWork {
	view := gen.PersonWork{
		Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
		Year: yearOf(row.Year), PrimaryType: row.PrimaryType, OnDisk: row.OnDisk,
		PresentCount: row.PresentCount, RequiredCount: row.RequiredCount,
		Status: gen.PersonWorkStatus(albumStatus(row, now)),
	}
	if row.WantedState != nil {
		state := gen.PersonWorkWantedState(*row.WantedState)
		view.WantedState = &state
	}
	if row.PosterURL != "" {
		view.PosterUrl = &row.PosterURL
	}
	return view
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
	if s.personRemover == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "people are not available")
		return
	}
	if err := s.personRemover.Remove(r.Context(), id, access.UserID()); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such person")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// personVisibility is the libraries the caller may read; false means the answer was already written.
func (s *Server) personVisibility(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return nil, false
	}
	_, categoryIDs := s.visibleAt(access, settings.PermissionRead)
	if categoryIDs == nil {
		categoryIDs = []int64{}
	}
	return categoryIDs, true
}
