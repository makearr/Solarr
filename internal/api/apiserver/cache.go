package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/platform/config"
)

// CacheBudget is ADR-60's own policy: a total byte ceiling, a per-entry age
// ceiling, and how the total splits between namespaces.
type CacheBudget struct {
	MaxBytes             int64
	MaxAgeDays           int64
	ImagesSharePercent   int64
	MetadataSharePercent int64
	// MinFreePercent is nil on a write to leave it alone.
	MinFreePercent *int64
}

// CacheReport is a budget plus what the last measurement found. The two
// byte fields and MeasuredAt are nil until the cache maintenance task has
// run at least once.
type CacheReport struct {
	Budget                     CacheBudget
	ImagesBytes, MetadataBytes *int64
	MeasuredAt                 *time.Time
	// DiskTotalBytes, DiskFreeBytes and ShrunkToBytes are nil until measured, ShrunkToBytes also when MaxBytes held.
	DiskTotalBytes, DiskFreeBytes, ShrunkToBytes *int64
	// The lookups the picture and provider response caches answered or missed since the process started.
	ImagesHits, ImagesMisses, MetadataHits, MetadataMisses int64
}

// DatabaseMemoryRow is one database's page cache and file sizes.
type DatabaseMemoryRow struct {
	Name                                string
	FileBytes, WALBytes                 int64
	PerConnectionKB, Connections        int64
	LimitBytes, UsedBytes, Hits, Misses int64
}

// DatabaseMemoryReader answers every database's page cache; cmd/solarr/adapters reads the stores.
type DatabaseMemoryReader interface {
	DatabaseMemory(ctx context.Context) []DatabaseMemoryRow
}

// The bounds database.cache_size_kb may be saved within.
const (
	minCacheSizeKB = 2048
	maxCacheSizeKB = 16 * 1024 * 1024
)

// CacheManager exposes ADR-60's cache budget, its last measurement, and the
// manual clear to the API. Defined here and implemented by a
// cmd/solarr adapter over internal/jobs/cachebudget.Sweep and
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
		MinFreePercent: body.MinFreePercent,
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
		MinFreePercent: derefOr(report.Budget.MinFreePercent, 0), DiskTotalBytes: report.DiskTotalBytes,
		DiskFreeBytes: report.DiskFreeBytes, ShrunkToBytes: report.ShrunkToBytes,
		ImagesHits: &report.ImagesHits, ImagesMisses: &report.ImagesMisses,
		MetadataHits: &report.MetadataHits, MetadataMisses: &report.MetadataMisses,
	}
}

// GetDatabaseMemory answers the page cache setting and each database's cache and file sizes.
func (s *Server) GetDatabaseMemory(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.databaseMemoryView(r.Context()))
}

// SetDatabaseMemory writes database.cache_size_kb into config.yml. Administrator only.
func (s *Server) SetDatabaseMemory(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.SetDatabaseMemoryJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected cache_size_kb")
		return
	}
	if body.CacheSizeKb < minCacheSizeKB || body.CacheSizeKb > maxCacheSizeKB {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request",
			fmt.Sprintf("cache_size_kb must be between %d and %d", minCacheSizeKB, maxCacheSizeKB))
		return
	}
	if err := s.patchConfig(func(data []byte) ([]byte, error) {
		return config.SetDatabaseCacheSizeKB(data, int(body.CacheSizeKb))
	}); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.databaseMemoryView(r.Context()))
}

func (s *Server) databaseMemoryView(ctx context.Context) gen.DatabaseMemory {
	configured := int64(config.DefaultCacheSizeKB)
	if data, err := s.readConfig(); err == nil {
		if loaded, err := config.Parse(data); err == nil {
			configured = int64(loaded.Database.CacheSizeKB)
		}
	}
	rows := []gen.DatabaseMemoryRow{}
	var running int64
	if s.databaseMemory != nil {
		for _, row := range s.databaseMemory.DatabaseMemory(ctx) {
			rows = append(rows, gen.DatabaseMemoryRow{
				Name: row.Name, FileBytes: row.FileBytes, WalBytes: row.WALBytes, PerConnectionKb: row.PerConnectionKB,
				Connections: row.Connections, LimitBytes: row.LimitBytes, UsedBytes: row.UsedBytes, Hits: row.Hits, Misses: row.Misses,
			})
			if row.Name == "library" {
				running = row.PerConnectionKB
			}
		}
	}
	return gen.DatabaseMemory{
		ConfiguredKb: configured, RunningKb: running, DefaultKb: config.DefaultCacheSizeKB,
		RestartRequired: running != 0 && running != configured, Databases: rows,
	}
}
