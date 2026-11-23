package apiserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// DiscoverItem is one recommendation as the pool holds it.
type DiscoverItem struct {
	ID, CategoryID                              int64
	Kind, Provider, ExternalID, Title, Subtitle string
	Overview, PosterURL                         string
	Year, Votes, WorkID                         int64
	Popularity, Rating                          float64
	Genres, Types                               []string
}

// DiscoverQuery asks one page of the pool for one person.
type DiscoverQuery struct {
	CategoryIDs              []int64
	Kind, Type, Genre, Title string
	UserID, Limit            int64
	HasCursor                bool
	AfterPopularity          float64
	AfterID                  int64
}

// DiscoverFeedState is how far a feed was read and how its last read ended.
type DiscoverFeedState struct {
	Type, Kind, LastError string
	Exhausted             bool
	RefreshedAt           int64
}

// DiscoverDismissal is one thing a person is not interested in.
type DiscoverDismissal struct {
	MediaType, Kind, Provider, ExternalID, Title string
	CreatedAt                                    int64
}

// DiscoverGenre is a genre of a library's pool and how many recommendations carry it.
type DiscoverGenre struct {
	Genre string
	Items int64
}

// DiscoverPool is what Discover's read API reads and writes; implemented in cmd/solarr/adapters over library.db.
type DiscoverPool interface {
	List(ctx context.Context, query DiscoverQuery) ([]DiscoverItem, error)
	Item(ctx context.Context, id int64) (DiscoverItem, bool, error)
	Genres(ctx context.Context, categoryID int64, kind string) ([]DiscoverGenre, error)
	FeedStates(ctx context.Context, categoryID int64) ([]DiscoverFeedState, error)
	Dismiss(ctx context.Context, userID int64, mediaType string, item DiscoverItem) error
	Restore(ctx context.Context, userID int64, kind, provider, externalID string) error
	Dismissals(ctx context.Context, userID int64) ([]DiscoverDismissal, error)
}

type discoverCursor struct {
	Popularity float64 `json:"p"`
	ID         int64   `json:"i"`
}

func encodeDiscoverCursor(item DiscoverItem) string {
	raw, _ := json.Marshal(discoverCursor{Popularity: item.Popularity, ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeDiscoverCursor(text string) (discoverCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(text)
	if err != nil {
		return discoverCursor{}, false
	}
	var cursor discoverCursor
	return cursor, json.Unmarshal(raw, &cursor) == nil
}

// discoverReady answers the caller's access when Discover is on and its pool is wired; otherwise the problem is written and false returned.
func (s *Server) discoverReady(w http.ResponseWriter, r *http.Request) (authz.Access, bool) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return authz.Access{}, false
	}
	if !s.auth.Settings.Snapshot().DiscoverGlobal().Enabled || s.discoverPool == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "recommendations are switched off")
		return authz.Access{}, false
	}
	return access, true
}

func recommendationView(item DiscoverItem) gen.Recommendation {
	view := gen.Recommendation{
		Id: item.ID, CategoryId: item.CategoryID, Kind: item.Kind, Provider: item.Provider, ExternalId: item.ExternalID, Title: item.Title,
		Subtitle: item.Subtitle, Overview: item.Overview, PosterUrl: item.PosterURL, Year: item.Year, Rating: item.Rating, Votes: item.Votes,
		Genres: nonNil(item.Genres), Types: nonNil(item.Types),
	}
	if item.WorkID != 0 {
		view.LibraryId = &item.WorkID
	}
	return view
}

// ListDiscoverLibraries answers the libraries the caller may read that have Discover on.
func (s *Server) ListDiscoverLibraries(w http.ResponseWriter, r *http.Request) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	configured, err := s.auth.Settings.LibraryDiscovers(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := []gen.DiscoverLibrary{}
	for _, one := range configured {
		if !one.Enabled || !access.CanSee(one.CategoryID) {
			continue
		}
		view := gen.DiscoverLibrary{
			CategoryId: one.CategoryID, Name: one.Name, MediaType: one.MediaType, TotalAmount: int(one.TotalAmount), Types: []string{}, Feeds: []gen.DiscoverFeed{},
			CanAdd:      access.Permission(one.CategoryID).AtLeast(settings.PermissionRequest),
			RequestOnly: !access.Permission(one.CategoryID).AtLeast(settings.PermissionManage),
		}
		for _, setting := range one.Types {
			if setting.Enabled {
				view.Types = append(view.Types, setting.Type)
			}
		}
		states, err := s.discoverPool.FeedStates(r.Context(), one.CategoryID)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		for _, state := range states {
			if slices.Contains(view.Types, state.Type) {
				view.Feeds = append(view.Feeds, gen.DiscoverFeed{Type: state.Type, Kind: state.Kind, Exhausted: state.Exhausted, LastError: state.LastError, RefreshedAt: state.RefreshedAt})
			}
		}
		out = append(out, view)
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// ListRecommendations answers a page of one library's recommendations.
func (s *Server) ListRecommendations(w http.ResponseWriter, r *http.Request, params gen.ListRecommendationsParams) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	if _, err := access.Category(params.CategoryId, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	limit := int64(min(max(derefOr(params.Limit, 40), 1), 100))
	query := DiscoverQuery{
		CategoryIDs: []int64{params.CategoryId}, Kind: derefOr(params.Kind, ""), Genre: derefOr(params.Genre, ""), Title: derefOr(params.Q, ""),
		UserID: access.UserID(), Limit: limit + 1,
	}
	if params.Type != nil {
		query.Type = string(*params.Type)
	}
	if params.Cursor != nil && *params.Cursor != "" {
		cursor, valid := decodeDiscoverCursor(*params.Cursor)
		if !valid {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "cursor is not one this server gave")
			return
		}
		query.HasCursor, query.AfterPopularity, query.AfterID = true, cursor.Popularity, cursor.ID
	}
	items, err := s.discoverPool.List(r.Context(), query)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.RecommendationPage{Items: []gen.Recommendation{}}
	if int64(len(items)) > limit {
		items = items[:limit]
		next := encodeDiscoverCursor(items[len(items)-1])
		page.NextCursor = &next
	}
	for _, item := range items {
		page.Items = append(page.Items, recommendationView(item))
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// ListRecommendationGenres answers the genre facet of one library.
func (s *Server) ListRecommendationGenres(w http.ResponseWriter, r *http.Request, params gen.ListRecommendationGenresParams) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	if _, err := access.Category(params.CategoryId, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	genres, err := s.discoverPool.Genres(r.Context(), params.CategoryId, derefOr(params.Kind, ""))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := make([]gen.RecommendationGenre, len(genres))
	for index, genre := range genres {
		out[index] = gen.RecommendationGenre{Genre: genre.Genre, Items: genre.Items}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// GetDiscoverDashboard answers one row of the caller's libraries interleaved, each one's most popular first.
func (s *Server) GetDiscoverDashboard(w http.ResponseWriter, r *http.Request, params gen.GetDiscoverDashboardParams) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	configured, err := s.auth.Settings.LibraryDiscovers(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	limit := min(max(derefOr(params.Limit, 20), 1), 100)
	var lists [][]DiscoverItem
	for _, one := range configured {
		if !one.Enabled || !access.CanSee(one.CategoryID) {
			continue
		}
		items, err := s.discoverPool.List(r.Context(), DiscoverQuery{CategoryIDs: []int64{one.CategoryID}, UserID: access.UserID(), Limit: int64(limit)})
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		lists = append(lists, items)
	}
	out := []gen.Recommendation{}
	for round := 0; len(out) < limit; round++ {
		took := false
		for _, list := range lists {
			if round < len(list) && len(out) < limit {
				out = append(out, recommendationView(list[round]))
				took = true
			}
		}
		if !took {
			break
		}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// DismissRecommendation hides a recommendation from the caller, in every library of theirs.
func (s *Server) DismissRecommendation(w http.ResponseWriter, r *http.Request, id int64) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	item, found, err := s.discoverPool.Item(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(item.CategoryID, settings.PermissionRead)
	if !found || err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such recommendation")
		return
	}
	if err := s.discoverPool.Dismiss(r.Context(), access.UserID(), category.MediaType, item); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListRecommendationDismissals answers what the caller dismissed.
func (s *Server) ListRecommendationDismissals(w http.ResponseWriter, r *http.Request) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	dismissals, err := s.discoverPool.Dismissals(r.Context(), access.UserID())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := make([]gen.RecommendationDismissal, len(dismissals))
	for index, one := range dismissals {
		out[index] = gen.RecommendationDismissal{MediaType: one.MediaType, Kind: one.Kind, Provider: one.Provider, ExternalId: one.ExternalID, Title: one.Title, CreatedAt: one.CreatedAt}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// RestoreRecommendation undoes one of the caller's dismissals.
func (s *Server) RestoreRecommendation(w http.ResponseWriter, r *http.Request, provider, externalID string, params gen.RestoreRecommendationParams) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	if err := s.discoverPool.Restore(r.Context(), access.UserID(), derefOr(params.Kind, ""), provider, externalID); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LoadMoreRecommendations reads the next provider page of a library's lists into the pool.
func (s *Server) LoadMoreRecommendations(w http.ResponseWriter, r *http.Request) {
	access, ok := s.discoverReady(w, r)
	if !ok {
		return
	}
	var body gen.LoadMoreRecommendationsJSONRequestBody
	if !s.decodeBody(w, r, maxUserBody, &body, "expected a JSON object") {
		return
	}
	if _, err := access.Category(body.CategoryId, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.discoverRefresher == nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "recommendations cannot be read further")
		return
	}
	listType := ""
	if body.Type != nil {
		listType = string(*body.Type)
	}
	added, exhausted, err := s.discoverRefresher.MoreDiscover(r.Context(), body.CategoryId, listType)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Added     int  `json:"added"`
		Exhausted bool `json:"exhausted"`
	}{added, exhausted})
}
