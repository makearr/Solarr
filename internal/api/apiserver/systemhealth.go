package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// HealthCheck is one thing wrong with this instance right now.
type HealthCheck struct {
	ID       string
	Severity string
	Message  string
	Link     string
}

// Severities. An error means something cannot work at all; a warning means it
// works but will not do what the user probably expects.
const (
	HealthError   = "error"
	HealthWarning = "warning"
)

// SystemHealthSource supplies the checks apiserver cannot compute from
// settings alone -- anything needing the library store, the job registry or
// the filesystem. Defined here and implemented by the caller, the same
// separation WorksReader keeps.
type SystemHealthSource interface {
	// Checks answers whatever this source currently has to report. An
	// implementation that cannot answer returns no checks rather than an
	// error: a health screen that fails to render because one probe failed is
	// worse than one missing probe.
	Checks(ctx context.Context) []HealthCheck
}

// GetSystemHealth reports configuration and operational problems, worst
// first. Distinct from GetLibraryHealth, which reports on the library's data
// rather than on the instance. Every check is computed fresh: this is read by
// a human looking at a dashboard, not on a hot path.
func (s *Server) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
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
		views = append(views, view)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// settingsHealthChecks are the checks answerable from the settings snapshot
// alone -- what the operator has and has not configured.
func (s *Server) settingsHealthChecks() []HealthCheck {
	snapshot := s.auth.Settings.Snapshot()
	var checks []HealthCheck

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
				ID: "category_without_root", Severity: HealthError,
				Message: fmt.Sprintf("The library %q has no root folder, so nothing can be scanned or imported into it.", category.Name),
				Link:    "/storage/folders",
			})
		}
	}
	for _, root := range snapshot.RootFolders() {
		switch {
		case !root.Online:
			checks = append(checks, HealthCheck{
				ID: "root_offline", Severity: HealthWarning,
				Message: fmt.Sprintf("The root folder %s is declared offline. Nothing on it counts as missing.", root.Path),
				Link:    "/storage/folders",
			})
		case !root.Writable && !root.ReadOnly:
			// Read-only is a deliberate policy; unwritable without it is not.
			checks = append(checks, HealthCheck{
				ID: "root_not_writable", Severity: HealthWarning,
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
