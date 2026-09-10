package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/platform/config"
)

// CacheBudget is ADR-60's own policy: a total byte ceiling, a per-entry age
// ceiling, and how the total splits between namespaces.
type CacheBudget struct {
	MaxBytes             int64
	MaxAgeDays           int64
	ImagesSharePercent   int64
	MetadataSharePercent int64
}

// CacheReport is a budget plus what the last measurement found. The two
// byte fields and MeasuredAt are nil until the cache maintenance task has
// run at least once.
type CacheReport struct {
	Budget                     CacheBudget
	ImagesBytes, MetadataBytes *int64
	MeasuredAt                 *time.Time
}

// CacheManager exposes ADR-60's cache budget, its last measurement, and the
// manual clear to the API. Defined here and implemented by a
// cmd/medianexus adapter over internal/jobs/cachebudget.Sweep and
// internal/db/settings.Cache, the same reasoning WorksReader's own doc comment
// gives for every other reader interface in this package.
type CacheManager interface {
	Report(ctx context.Context) (CacheReport, error)
	// SetBudget writes a new budget (ADR-44: validated on write).
	SetBudget(ctx context.Context, budget CacheBudget) error
	// Clear removes everything in both namespaces (Invariant 5: always safe).
	Clear(ctx context.Context) error
}

// GetCache answers the cache's own budgets and last-measured size.
func (s *Server) GetCache(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.cacheManager == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the cache is not available")
		return
	}

	report, err := s.cacheManager.Report(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, cacheReportView(report))
}

// UpdateCacheBudget changes the cache's budgets. Administrator only.
func (s *Server) UpdateCacheBudget(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.cacheManager == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the cache is not available")
		return
	}

	var body gen.UpdateCacheBudgetJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected max_bytes, max_age_days, images_share_percent and metadata_share_percent")
		return
	}

	budget := CacheBudget{
		MaxBytes: body.MaxBytes, MaxAgeDays: body.MaxAgeDays,
		ImagesSharePercent: body.ImagesSharePercent, MetadataSharePercent: body.MetadataSharePercent,
	}
	if err := s.cacheManager.SetBudget(r.Context(), budget); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The budget has problems", problems.Report("the cache budget", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}

	report, err := s.cacheManager.Report(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, cacheReportView(report))
}

// ClearCache removes everything in both namespaces immediately.
// Administrator only. Always safe (Invariant 5).
func (s *Server) ClearCache(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.cacheManager == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the cache is not available")
		return
	}

	if err := s.cacheManager.Clear(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}

	report, err := s.cacheManager.Report(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, cacheReportView(report))
}

func cacheReportView(report CacheReport) gen.CacheReport {
	return gen.CacheReport{
		MaxBytes: report.Budget.MaxBytes, MaxAgeDays: report.Budget.MaxAgeDays,
		ImagesSharePercent: report.Budget.ImagesSharePercent, MetadataSharePercent: report.Budget.MetadataSharePercent,
		ImagesBytes: report.ImagesBytes, MetadataBytes: report.MetadataBytes, MeasuredAt: report.MeasuredAt,
	}
}
