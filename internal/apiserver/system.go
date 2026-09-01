package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/settings"
)

// BuildInfo is what the About endpoint reports. An unknown commit is reported
// as unknown rather than as a plausible-looking wrong value, because the source
// link depends on it (AGPL section 13).
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	// SourceURL points at the Corresponding Source for *this* build. A modified
	// build must not point at upstream: that is precisely the misrepresentation
	// section 13 exists to prevent (documentation/licence-compliance.md).
	SourceURL string `json:"source_url"`
	License   string `json:"license"`
}

// Checker reports whether the process is alive and whether it is ready to serve.
//
// An interface rather than a concrete dependency, so apiserver does not import
// every subsystem it reports on -- and so a test can make either answer fail
// without arranging a broken database.
type Checker interface {
	// Live reports that the process is working: the write queues are not
	// wedged. It must not depend on plugins or external services, or a dead
	// indexer restarts the container (section 20.2).
	Live(ctx context.Context) error
	// Ready reports that the databases are open, migrated and answering.
	Ready(ctx context.Context) error
}

// healthTimeout bounds a probe. An orchestrator's own timeout is usually 5s, so
// answering slowly is as bad as not answering.
const healthTimeout = 3 * time.Second

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := s.checker.Live(ctx); err != nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Not healthy", err.Error())
		return
	}
	writeJSON(w, s.logger, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
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

// handleAbout is unauthenticated by design. AGPL section 13 obliges the running
// program to offer its source to "all users interacting with it remotely",
// which is not limited to those who can log in.
func (s *Server) handleAbout(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, s.build)
}

// handleMe reports who the caller is and what they can see. It is the smallest
// authenticated endpoint, and the one a client uses to discover its own
// permissions rather than guessing from failed requests.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
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
