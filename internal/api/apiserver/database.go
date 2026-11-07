package apiserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// ErrNoValidBackup means the newest backup failed its check, so nothing older may be removed.
var ErrNoValidBackup = errors.New("apiserver: the newest backup is not valid")

// DatabaseUsage is where Solarr's own folders spend disk space.
type DatabaseUsage struct {
	MediaCoverBytes, PreviewsBytes, CacheBytes, BackupsBytes, BackupCount int64
	LatestBackupAt                                                        *time.Time
}

// DatabaseAdmin is the expert Database page: sizes and the backup cleanup. Implemented in cmd/solarr/app.
type DatabaseAdmin interface {
	Usage(ctx context.Context) (DatabaseUsage, error)
	// ClearOldBackups verifies the newest backup, then removes the older ones; answers how many and the name kept.
	ClearOldBackups(ctx context.Context) (removed int64, kept string, err error)
}

// GetDatabaseUsage answers the folder sizes and the newest backup.
func (s *Server) GetDatabaseUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.databaseAdmin == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the database page is not available")
		return
	}
	usage, err := s.databaseAdmin.Usage(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.DatabaseUsage{
		MediaCoverBytes: usage.MediaCoverBytes, PreviewsBytes: usage.PreviewsBytes, CacheBytes: usage.CacheBytes,
		BackupsBytes: usage.BackupsBytes, BackupCount: usage.BackupCount, LatestBackupAt: usage.LatestBackupAt,
	})
}

// ClearOldBackups keeps only the newest backup once it has been checked.
func (s *Server) ClearOldBackups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.databaseAdmin == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the database page is not available")
		return
	}
	removed, kept, err := s.databaseAdmin.ClearOldBackups(r.Context())
	if errors.Is(err, ErrNoValidBackup) {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "The newest backup is not valid", err.Error())
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ClearedBackups{Removed: removed, Kept: kept})
}
