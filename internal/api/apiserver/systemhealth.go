package apiserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// HealthCheck is one thing wrong with this instance right now.
type HealthCheck struct {
	ID       string
	Severity string
	Message  string
	Link     string
	// Until is when a check that clears itself is next tried; the web formats it.
	Until *time.Time
}

// Severities. An error means something cannot work at all; a warning means it
// works but will not do what the user probably expects.
const (
	HealthError   = "error"
	HealthWarning = "warning"
)

// SystemHealthSource supplies checks needing the library store, job registry or filesystem; implemented by the caller.
type SystemHealthSource interface {
	// Checks returns what the source can report; an unanswerable probe yields no check rather than failing the screen.
	Checks(ctx context.Context) []HealthCheck
}

// GetSystemHealth reports instance problems worst first, computed fresh; GetLibraryHealth covers the library's data instead.
func (s *Server) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
	// The checks name configuration and paths only an administrator may see.
	if !s.requireAdministrator(w, r) {
		return
	}

	checks := s.settingsHealthChecks()
	if s.systemHealth != nil {
		checks = append(checks, s.systemHealth.Checks(r.Context())...)
	}

	views := make([]gen.HealthCheck, 0, len(checks))
	for _, check := range errorsFirst(checks) {
		view := gen.HealthCheck{
			Id: check.ID, Severity: gen.HealthCheckSeverity(check.Severity), Message: check.Message,
		}
		if check.Link != "" {
			view.Link = &check.Link
		}
		view.Until = check.Until
		views = append(views, view)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// settingsHealthChecks are the checks answerable from the settings snapshot
// alone -- what the operator has and has not configured.
func (s *Server) settingsHealthChecks() []HealthCheck {
	snapshot := s.auth.Settings.Snapshot()
	var checks []HealthCheck

	if !snapshot.AuthRequired() {
		checks = append(checks, HealthCheck{
			ID: "sign_in_off", Severity: HealthWarning,
			Message: "Signing in is switched off, so anyone who can reach this port is the administrator.",
			Link:    "/settings/user",
		})
	}

	categories := snapshot.Categories()
	if len(categories) == 0 {
		checks = append(checks, HealthCheck{
			ID: "no_library", Severity: HealthError,
			Message: "No library has been created yet, so there is nowhere to put anything.",
			Link:    "/libraries",
		})
	}
	for _, category := range categories {
		if len(snapshot.RootFoldersForCategory(category.ID)) == 0 {
			checks = append(checks, HealthCheck{
				ID: fmt.Sprintf("category_without_root_%d", category.ID), Severity: HealthError,
				Message: fmt.Sprintf("The library %q has no root folder, so nothing can be scanned or imported into it.", category.Name),
				Link:    "/storage/folders",
			})
		}
	}
	for _, root := range snapshot.RootFolders() {
		switch {
		case !root.Online:
			checks = append(checks, HealthCheck{
				ID: fmt.Sprintf("root_offline_%d", root.ID), Severity: HealthWarning,
				Message: fmt.Sprintf("The root folder %s is declared offline. Nothing on it counts as missing.", root.Path),
				Link:    "/storage/folders",
			})
		case !root.Writable && !root.ReadOnly:
			// Read-only is a deliberate policy; unwritable without it is not.
			checks = append(checks, HealthCheck{
				ID: fmt.Sprintf("root_not_writable_%d", root.ID), Severity: HealthWarning,
				Message: fmt.Sprintf("The root folder %s cannot be written to, and is not marked read-only. Check its ownership, or whether it is a read-only mount.", root.Path),
				Link:    "/storage/folders",
			})
		}
	}
	return checks
}

// errorsFirst orders errors before warnings, each group keeping the order it
// was produced in so the same instance always reports the same list.
func errorsFirst(checks []HealthCheck) []HealthCheck {
	ordered := make([]HealthCheck, 0, len(checks))
	for _, check := range checks {
		if check.Severity == HealthError {
			ordered = append(ordered, check)
		}
	}
	for _, check := range checks {
		if check.Severity != HealthError {
			ordered = append(ordered, check)
		}
	}
	return ordered
}
