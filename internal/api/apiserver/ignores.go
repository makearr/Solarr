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
	// IgnoreHeld ignores (or, ignored false, stops ignoring) a work or person the library holds under every id it has.
	IgnoreHeld(ctx context.Context, userID int64, person bool, id int64, mediaType string, ignored bool, at time.Time) error
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

// IgnoreHeld ignores a work or person the library holds, in the caller's name or for everyone, under every provider id it has; a held
// work is also left unmonitored when the caller may change that, so no list offers it as missing.
func (s *Server) IgnoreHeld(w http.ResponseWriter, r *http.Request) {
	var body gen.IgnoreHeldRequest
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected category_id, subject and id") {
		return
	}
	s.changeHeldIgnore(w, r, body, true)
}

// UnignoreHeld undoes IgnoreHeld; the work stays unmonitored until someone monitors it.
func (s *Server) UnignoreHeld(w http.ResponseWriter, r *http.Request) {
	var body gen.IgnoreHeldRequest
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected category_id, subject and id") {
		return
	}
	s.changeHeldIgnore(w, r, body, false)
}

func (s *Server) changeHeldIgnore(w http.ResponseWriter, r *http.Request, body gen.IgnoreHeldRequest, ignored bool) {
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
	person := body.Subject == gen.IgnoreHeldRequestSubjectPerson
	if err := s.ignores.IgnoreHeld(r.Context(), owner, person, body.Id, category.MediaType, ignored, s.auth.Clock.Now()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if ignored && !person && s.worksMonitor != nil && access.Permission(body.CategoryId).AtLeast(settings.PermissionAdd) {
		if _, err := s.worksMonitor.MonitorWork(r.Context(), body.Id, MonitorScope{Monitored: false}); err != nil {
			s.logger.WarnContext(r.Context(), "ignored work left monitored", "work_id", body.Id, "error", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
