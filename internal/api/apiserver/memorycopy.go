package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// The two ways a RAM copy is refused.
var (
	ErrNotEnoughMemory     = errors.New("apiserver: the container does not have the memory")
	ErrRiskNotAcknowledged = errors.New("apiserver: the risk was not acknowledged")
)

// MemoryCopyStatus is the cache and database RAM options as configured, as running, and what they would cost.
type MemoryCopyStatus struct {
	CacheEnabled, DatabaseEnabled, CacheRunning, DatabaseRunning bool
	CacheSyncMinutes, DatabaseSyncMinutes                        int64
	// BytesNeeded include the headroom.
	CacheBytesNeeded, DatabaseBytesNeeded      int64
	AvailableBytes, LimitBytes, TmpfsFreeBytes int64
	// TmpfsTotalBytes is the size of the RAM directory; CacheDiskBytes and DatabaseDiskBytes what each option would hold.
	TmpfsTotalBytes, CacheDiskBytes, DatabaseDiskBytes int64
	MemorySource, TmpfsDir, Notice                     string
	LastCacheSync, LastDatabaseSync                    *time.Time
}

// MemoryCopySettings is a change to them.
type MemoryCopySettings struct {
	CacheEnabled, DatabaseEnabled         bool
	CacheSyncMinutes, DatabaseSyncMinutes int64
	// Acknowledged is the person confirming they understand the risk; required to turn either on.
	Acknowledged bool
}

// MemoryCopies is the Cache page's RAM options. Implemented in cmd/solarr/app.
type MemoryCopies interface {
	Status(ctx context.Context) (MemoryCopyStatus, error)
	// Set refuses to turn an option on without the acknowledgement or without the memory; it applies at the next start.
	Set(ctx context.Context, change MemoryCopySettings) error
}

// GetMemoryCopies answers the RAM options. Administrator only.
func (s *Server) GetMemoryCopies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	s.writeMemoryCopies(w, r)
}

// SetMemoryCopies changes the RAM options. Administrator only.
func (s *Server) SetMemoryCopies(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.memoryCopies == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "RAM copies are not available")
		return
	}
	var body gen.MemoryCopySettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the RAM options")
		return
	}
	err := s.memoryCopies.Set(r.Context(), MemoryCopySettings{
		CacheEnabled: body.CacheEnabled, DatabaseEnabled: body.DatabaseEnabled,
		CacheSyncMinutes: body.CacheSyncMinutes, DatabaseSyncMinutes: body.DatabaseSyncMinutes, Acknowledged: body.Acknowledged,
	})
	switch {
	case errors.Is(err, ErrRiskNotAcknowledged):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Confirm the risk", "tick \"I understand the risk\" to turn a RAM copy on")
		return
	case errors.Is(err, ErrNotEnoughMemory):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not enough memory", err.Error())
		return
	case err != nil:
		writeError(w, s.logger, err)
		return
	}
	s.writeMemoryCopies(w, r)
}

func (s *Server) writeMemoryCopies(w http.ResponseWriter, r *http.Request) {
	if s.memoryCopies == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "RAM copies are not available")
		return
	}
	status, err := s.memoryCopies.Status(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.MemoryCopyStatus{
		CacheEnabled: status.CacheEnabled, DatabaseEnabled: status.DatabaseEnabled,
		CacheRunning: status.CacheRunning, DatabaseRunning: status.DatabaseRunning,
		CacheSyncMinutes: status.CacheSyncMinutes, DatabaseSyncMinutes: status.DatabaseSyncMinutes,
		CacheBytesNeeded: status.CacheBytesNeeded, DatabaseBytesNeeded: status.DatabaseBytesNeeded,
		AvailableBytes: status.AvailableBytes, LimitBytes: status.LimitBytes, TmpfsFreeBytes: status.TmpfsFreeBytes,
		TmpfsTotalBytes: status.TmpfsTotalBytes, CacheDiskBytes: status.CacheDiskBytes, DatabaseDiskBytes: status.DatabaseDiskBytes,
		MemorySource: status.MemorySource, TmpfsDir: status.TmpfsDir, LastCacheSync: status.LastCacheSync,
		LastDatabaseSync: status.LastDatabaseSync,
		RestartRequired:  status.CacheEnabled != status.CacheRunning || status.DatabaseEnabled != status.DatabaseRunning,
	}
	if status.Notice != "" {
		view.Notice = &status.Notice
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}
