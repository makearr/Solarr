package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
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

	writeJSON(w, s.logger, http.StatusOK, meView{
		UserID:     access.UserID(),
		Role:       string(access.Role()),
		Categories: categories,
	})
}

type meView struct {
	UserID     int64          `json:"user_id"`
	Role       string         `json:"role"`
	Categories []categoryView `json:"categories"`
}

type categoryView struct {
	ID        int64  `json:"id"`
	MediaType string `json:"media_type"`
	Name      string `json:"name"`
	// Permission is what this caller may do here, so a client does not have to
	// discover its limits by being refused.
	Permission string `json:"permission"`
}
