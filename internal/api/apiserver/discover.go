package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// ErrDiscoverUnsupportedMediaType reports a category whose media type has no
// search provider wired yet -- today, everything but music (ADR-56's "per
// library" half only; the global, cross-media-type half with its
// destination-suggestion ladder is not built, todo 4.17).
var ErrDiscoverUnsupportedMediaType = errors.New("apiserver: no search provider for this category's media type")

// DiscoverKind is what a discover result is: an album becomes a monitored
// desired item, an artist a monitored person with a subscription.
type DiscoverKind string

// The two kinds discover can search for and add.
const (
	DiscoverAlbum  DiscoverKind = "album"
	DiscoverArtist DiscoverKind = "artist"
)

// DiscoverResult is one candidate a search found, not yet in the library.
type DiscoverResult struct {
	Kind       DiscoverKind
	Provider   string
	ExternalID string
	Title      string
	Artist     string
	// Disambiguation is the provider's own short note telling two same-named
	// artists apart, empty when it offers none.
	Disambiguation string
	// Year is nil when the provider's own answer carries no release date at
	// all (ADR-56 does not ask for day precision here, todo 4.20's own
	// FreshSince is a different, later concern).
	Year *int64
}

// DiscoverAddResult is what adding a result created or reused (ADR-56: "the
// result is a resolved work plus a desired_item", never a download).
type DiscoverAddResult struct {
	// Kind says which ids below are populated.
	Kind          DiscoverKind
	WorkID        int64
	DesiredItemID int64
	// PersonID and SubscriptionID are an artist add's own result: the albums
	// themselves arrive when the subscription expands, not here.
	PersonID       int64
	SubscriptionID int64
	// Priority is ADR-36's composite score, computed as a preview against
	// TriggerUserRequest (ADR-56's own "shows the priority... the item will
	// get") -- there is no real hunt_queue yet to report a rank within
	// (stage 5/6), so this is the score alone, not a position.
	Priority int64
}

// Discover searches a category's own metadata provider and adds a result as
// intent (ADR-56's per-library half; the global half is not built). Defined
// here and implemented by the caller, so this package never imports the
// library store or a metadata provider directly (WorksReader's own doc
// comment gives the same reasoning).
type Discover interface {
	// Search answers up to limit candidates for categoryID's media type.
	// ErrQueryTooShort for a query under section 21.5's floor,
	// ErrDiscoverUnsupportedMediaType when that media type has no provider.
	Search(ctx context.Context, categoryID int64, kind DiscoverKind, query string, limit int) ([]DiscoverResult, error)
	// Add resolves-or-creates the work behind (provider, externalID) and
	// ensures a monitored album-level desired_item for it, idempotently
	// (Invariant 71 -- re-adding an existing result is not an error).
	// search starts looking for what was added: an album at once, an artist once their albums are in.
	Add(ctx context.Context, categoryID, userID int64, provider, externalID string, monitored, search bool) (DiscoverAddResult, error)
}

// routeDiscoverSearch parses category_id/q/limit into gen.DiscoverSearchParams
// by hand, mirroring routeListWorks -- routing stays hand-written so the
// authenticated group keeps its meaning (ADR-31).
func (s *Server) routeDiscoverSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "category_id is required and must be a number")
		return
	}
	params := gen.DiscoverSearchParams{CategoryId: categoryID, Q: query.Get("q")}
	if raw := query.Get("kind"); raw != "" {
		kind := gen.DiscoverKind(raw)
		if kind != gen.DiscoverKindAlbum && kind != gen.DiscoverKindArtist {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "kind must be album or artist")
			return
		}
		params.Kind = &kind
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.DiscoverSearch(w, r, params)
}

// DiscoverSearch answers a category's own metadata provider search (ADR-56's
// per-library half).
func (s *Server) DiscoverSearch(w http.ResponseWriter, r *http.Request, params gen.DiscoverSearchParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(params.CategoryId, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.discover == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "discover is not available")
		return
	}

	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	kind := DiscoverAlbum
	if params.Kind != nil {
		kind = DiscoverKind(*params.Kind)
	}
	results, err := s.discover.Search(r.Context(), params.CategoryId, kind, params.Q, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.DiscoverResult, len(results))
	for i, result := range results {
		var artist *string
		if result.Artist != "" {
			artist = &result.Artist
		}
		var disambiguation *string
		if result.Disambiguation != "" {
			disambiguation = &result.Disambiguation
		}
		viewKind := gen.DiscoverKind(result.Kind)
		views[i] = gen.DiscoverResult{
			Kind: &viewKind, Provider: result.Provider, ExternalId: result.ExternalID,
			Title: result.Title, Artist: artist, Year: result.Year,
			Disambiguation: disambiguation,
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// DiscoverAdd decodes the body by hand, same as SubscribePerson/ImportSettings (ADR-31).
func (s *Server) DiscoverAdd(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body gen.DiscoverAddRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}
	if body.Provider == "" || body.ExternalId == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "provider and external_id are required")
		return
	}
	// Manage, not Read: adding creates a new monitored item, the same class
	// of change PermissionManage's own doc comment already covers ("adds
	// monitoring, editing, upgrading and deleting").
	if _, err := access.Category(body.CategoryId, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.discover == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "discover is not available")
		return
	}

	result, err := s.discover.Add(r.Context(), body.CategoryId, access.UserID(), body.Provider,
		body.ExternalId, derefOr(body.Monitored, true), derefOr(body.Search, false))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.DiscoverAddResult{Kind: gen.DiscoverKind(result.Kind), Priority: result.Priority}
	// Only the ids this kind actually produced are reported, so a client never
	// reads a zero as a real row.
	if result.WorkID != 0 {
		view.WorkId = &result.WorkID
	}
	if result.DesiredItemID != 0 {
		view.DesiredItemId = &result.DesiredItemID
	}
	if result.PersonID != 0 {
		view.PersonId = &result.PersonID
	}
	if result.SubscriptionID != 0 {
		view.SubscriptionId = &result.SubscriptionID
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
