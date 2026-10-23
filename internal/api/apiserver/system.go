package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/apiserver/legal"
	"github.com/makearr/Solarr/internal/api/auth"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// BuildInfo is what the About endpoint reports (section 13).
type BuildInfo struct {
	Version string `json:"version"`
	// Milestone is a name, not a version component, and empty until named
	// (documentation/versioning.md) -- set by `make milestone NAME="name"`.
	Milestone string `json:"milestone,omitempty"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	// SourceURL points at the Corresponding Source for *this* build (section 13).
	SourceURL string `json:"source_url"`
	License   string `json:"license"`
	// SQLiteDriver is a build tag's doing, not a setting's (ADR-5).
	SQLiteDriver string `json:"sqlite_driver"`

	// ServerTime and Timezone are filled per response, not at build time: the
	// status bar shows the clock schedules actually run on, which on a remote
	// host is rarely the viewer's (section 19.2).
	ServerTime string `json:"server_time,omitempty"`
	Timezone   string `json:"timezone,omitempty"`
}

// Checker reports whether the process is alive and whether it is ready to serve.
type Checker interface {
	// Live reports that the process is working: the write queues are not wedged (section 20.2).
	Live(ctx context.Context) error
	// Ready reports that the databases are open, migrated and answering.
	Ready(ctx context.Context) error
}

// healthTimeout bounds a probe. An orchestrator's own timeout is usually 5s, so
// answering slowly is as bad as not answering.
const healthTimeout = 3 * time.Second

func (s *Server) GetHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := s.checker.Live(ctx); err != nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Not healthy", err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) GetReady(w http.ResponseWriter, r *http.Request) {
	// Readiness is false during startup and shutdown, so an orchestrator stops
	// sending work before the process stops accepting it (section 15.2).
	if !s.ready.Load() {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Not ready", "the service is starting up or shutting down")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := s.checker.Ready(ctx); err != nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Not ready", err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"status": "ready"})
}

// GetAbout is unauthenticated by design (section 13). It carries the clock too,
// so the status bar reads correctly before anyone has logged in.
func (s *Server) GetAbout(w http.ResponseWriter, _ *http.Request) {
	about := s.build
	now := s.wall.Now()
	about.ServerTime = now.Format(timeFormat)
	about.Timezone = s.timezone
	if about.Timezone == "" {
		about.Timezone = now.Location().String()
	}
	writeJSON(w, s.logger, http.StatusOK, about)
}

// GetAboutLicense serves the full AGPL-3.0-or-later text (section 13).
// Unauthenticated for the same reason as GetAbout.
func (s *Server) GetAboutLicense(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, gen.LegalText{Text: legal.License})
}

// GetAboutAttribution serves third-party attributions (ADR-18, section 13).
func (s *Server) GetAboutAttribution(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, gen.LegalText{Text: legal.Attribution})
}

// GetMe reports who the caller is and what they can see.
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	visible := access.Categories(settings.PermissionRead)
	categories := make([]categoryView, 0, len(visible))
	for _, category := range visible {
		categories = append(categories, categoryView{
			ID:         category.ID,
			MediaType:  category.MediaType,
			Name:       category.Name,
			Permission: string(access.Permission(category.ID)),
		})
	}

	tierMax, username := settings.TierExpert, ""
	snapshot := s.auth.Settings.Snapshot()
	if user, found := snapshot.User(access.UserID()); found {
		tierMax, username = user.Limits.TierMax, user.Username
	}
	writeJSON(w, s.logger, http.StatusOK, meView{
		UserID: access.UserID(), Username: username, AuthRequired: snapshot.AuthRequired(),
		Role:       string(access.Role()),
		Tier:       string(access.Tier()),
		TierMax:    string(tierMax),
		Categories: categories,
	})
}

// SetMyTier changes the caller's own tier (ADR-26) -- self-service, no id to
// parse, so unlike a PATCH on another resource this needs no routeX wrapper.
func (s *Server) SetMyTier(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with tier")
		return
	}

	if err := s.auth.Settings.SetUserTier(r.Context(), access.UserID(), settings.Tier(body.Tier)); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The tier has problems", problems.Report("the tier", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	// Not s.GetMe: access is frozen for the request's lifetime (ADR-27), so
	// reading the tier back off the same Access this handler already holds
	// would show what it was when the request began, not what was just
	// written -- the exact staleness class the category-editing endpoint
	// hit live (2026-09-12). User (unlike Categories/Category/
	// VisibleCategoryIDs/Permission) carries no per-caller filtering, so a
	// direct fresh Snapshot read is not the archtest-forbidden pattern.
	visible := access.Categories(settings.PermissionRead)
	categories := make([]categoryView, 0, len(visible))
	for _, category := range visible {
		categories = append(categories, categoryView{
			ID: category.ID, MediaType: category.MediaType, Name: category.Name,
			Permission: string(access.Permission(category.ID)),
		})
	}
	user, ok := s.auth.Settings.Snapshot().User(access.UserID())
	if !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such user")
		return
	}
	writeJSON(w, s.logger, http.StatusOK, meView{
		UserID: access.UserID(), Role: string(access.Role()), Tier: string(user.Tier),
		TierMax: string(user.Limits.TierMax), Categories: categories,
	})
}

type meView struct {
	UserID       int64          `json:"user_id"`
	Username     string         `json:"username,omitempty"`
	AuthRequired bool           `json:"auth_required"`
	Role         string         `json:"role"`
	Tier         string         `json:"tier"`
	TierMax      string         `json:"tier_max"`
	Categories   []categoryView `json:"categories"`
}

// ListMyAPIKeys lists the caller's own non-interactive credentials --
// self-service, same shape as SetMyTier. Never the key or its hash.
func (s *Server) ListMyAPIKeys(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	keys, err := s.auth.Settings.ListAPIKeys(r.Context(), access.UserID())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildAPIKeyViews(keys))
}

// CreateMyAPIKey mints a key for the caller and returns its plaintext; RevealMyAPIKey shows it again.
func (s *Server) CreateMyAPIKey(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with name")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a name is required")
		return
	}

	plaintext, hash, err := auth.GenerateAPIKey()
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	created, err := s.auth.Settings.CreateAPIKey(r.Context(), s.wall, access.UserID(), name, hash, plaintext)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	view := buildAPIKeyView(created)
	writeJSON(w, s.logger, http.StatusCreated, gen.CreatedAPIKey{
		Id: view.Id, Name: view.Name, CreatedAt: view.CreatedAt, LastUsedAt: view.LastUsedAt, Revealable: view.Revealable,
		Key: string(plaintext),
	})
}

// routeDeleteMyAPIKey parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeDeleteMyAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "an api key id must be a number")
		return
	}
	s.DeleteMyAPIKey(w, r, id)
}

// DeleteMyAPIKey revokes one of the caller's own keys. Refused (404, via
// writeError's settings.ErrNotFound mapping) for a key belonging to someone
// else -- settings.Cache.DeleteAPIKey checks ownership.
func (s *Server) DeleteMyAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if err := s.auth.Settings.DeleteAPIKey(r.Context(), access.UserID(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routeRevealMyAPIKey parses the id path parameter, mirroring routeDeleteMyAPIKey.
func (s *Server) routeRevealMyAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "an api key id must be a number")
		return
	}
	s.RevealMyAPIKey(w, r, id)
}

// RevealMyAPIKey shows one of the caller's own keys again (user, 2026-10-23); someone else's is 404.
func (s *Server) RevealMyAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	key, err := s.auth.Settings.RevealAPIKey(r.Context(), access.UserID(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"key": key.Reveal()})
}

func buildAPIKeyViews(keys []settings.APIKey) []gen.APIKey {
	views := make([]gen.APIKey, len(keys))
	for index, key := range keys {
		views[index] = buildAPIKeyView(key)
	}
	return views
}

func buildAPIKeyView(key settings.APIKey) gen.APIKey {
	view := gen.APIKey{Id: key.ID, Name: key.Name, CreatedAt: key.CreatedAt, Revealable: key.Revealable}
	if !key.LastUsedAt.IsZero() {
		lastUsed := key.LastUsedAt
		view.LastUsedAt = &lastUsed
	}
	return view
}

type categoryView struct {
	ID        int64  `json:"id"`
	MediaType string `json:"media_type"`
	Name      string `json:"name"`
	// Permission is what this caller may do here, so a client does not have to
	// discover its limits by being refused.
	Permission string `json:"permission"`
}

// GetMyPreferences answers the caller's own interface state: sort order,
// chosen columns and the like, each value raw JSON the client itself wrote.
func (s *Server) GetMyPreferences(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	preferences, err := s.auth.Settings.UserPreferences(r.Context(), access.UserID())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, preferences)
}

func (s *Server) routeSetMyPreference(w http.ResponseWriter, r *http.Request) {
	s.SetMyPreference(w, r, chi.URLParam(r, "key"))
}

// SetMyPreference stores one of them. Self-service, like SetMyTier.
func (s *Server) SetMyPreference(w http.ResponseWriter, r *http.Request, key string) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with value")
		return
	}

	if err := s.auth.Settings.SetUserPreference(r.Context(), access.UserID(), key, body.Value); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The preference has problems", problems.Report("the preference", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
