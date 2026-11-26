package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// IgnoreStore keeps what people ignore: an item a library lacks that is never to be offered as missing again.
type IgnoreStore interface {
	Ignore(ctx context.Context, userID int64, mediaType, kind, provider, externalID, title string, at time.Time) error
	Unignore(ctx context.Context, userID int64, kind, provider, externalID string) error
	// IgnoredKeys are "provider:external id" of what the person ignores of a media type, and an administrator for everyone.
	IgnoredKeys(ctx context.Context, userID int64, mediaType string) (map[string]bool, error)
}

// IgnoreAnyKind is the kind an ignore made from a list carries; Discover's own dismissals name the recommendation's kind.
const IgnoreAnyKind = "any"

// everyoneID is the ignore owner that stands for every account.
const everyoneID int64 = 0

// CreateIgnore ignores an item for the caller, or for everyone when an administrator asks.
func (s *Server) CreateIgnore(w http.ResponseWriter, r *http.Request) {
	var body gen.CreateIgnoreJSONRequestBody
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected category_id, kind, provider, external_id and title") {
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(body.CategoryId, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	owner, ok := s.ignoreOwner(w, r, access, derefOr(body.Everyone, false))
	if !ok {
		return
	}
	if s.ignores == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "ignoring is not available")
		return
	}
	if err := s.ignores.Ignore(r.Context(), owner, category.MediaType, body.Kind, body.Provider, body.ExternalId, body.Title, s.auth.Clock.Now()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteIgnore undoes an ignore of the caller's, or of everyone's for an administrator.
func (s *Server) DeleteIgnore(w http.ResponseWriter, r *http.Request, params gen.DeleteIgnoreParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	owner, ok := s.ignoreOwner(w, r, access, derefOr(params.Everyone, false))
	if !ok {
		return
	}
	if s.ignores == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "ignoring is not available")
		return
	}
	if err := s.ignores.Unignore(r.Context(), owner, params.Kind, params.Provider, params.ExternalId); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ignoreOwner is who an ignore belongs to: the caller, or everyone when an administrator asks for that.
func (s *Server) ignoreOwner(w http.ResponseWriter, r *http.Request, access authz.Access, everyone bool) (int64, bool) {
	if everyone {
		if !s.requireAdministrator(w, r) {
			return 0, false
		}
		return everyoneID, true
	}
	return access.UserID(), true
}

// ignoredIn is what the caller ignores of a media type; nothing when ignoring is not available or cannot be read.
func (s *Server) ignoredIn(ctx context.Context, mediaType string) map[string]bool {
	if s.ignores == nil {
		return nil
	}
	access, err := authz.FromContext(ctx)
	if err != nil {
		return nil
	}
	keys, err := s.ignores.IgnoredKeys(ctx, access.UserID(), mediaType)
	if err != nil {
		s.logger.WarnContext(ctx, "ignored items unavailable", "error", err)
		return nil
	}
	return keys
}

// ignoredKey is how one item is looked up among the ignored.
func ignoredKey(provider, externalID string) string { return provider + ":" + externalID }
