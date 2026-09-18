package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
)

// BackupFileRow is one backup on disk.
type BackupFileRow struct {
	Name         string
	SizeBytes    int64
	TakenAt      time.Time
	PreMigration bool
}

// Backups lists and takes backups (ADR-15). Restoring is deliberately absent:
// it stays the documented offline procedure. Defined here and implemented by
// the caller, the same separation WorksReader keeps.
type Backups interface {
	List(ctx context.Context) ([]BackupFileRow, error)
	// RunNow backs up every database and answers what it wrote.
	RunNow(ctx context.Context) ([]BackupFileRow, error)
}

// ListBackups answers the backup files on disk, newest first.
func (s *Server) ListBackups(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.backups == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "backups are not available")
		return
	}
	rows, err := s.backups.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, backupViews(rows))
}

// RunBackupNow backs up every database synchronously: a backup that has not
// finished is not one a user should be told about.
func (s *Server) RunBackupNow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.backups == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "backups are not available")
		return
	}
	rows, err := s.backups.RunNow(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, backupViews(rows))
}

func backupViews(rows []BackupFileRow) []gen.BackupFile {
	views := make([]gen.BackupFile, len(rows))
	for i, row := range rows {
		preMigration := row.PreMigration
		views[i] = gen.BackupFile{
			Name: row.Name, SizeBytes: row.SizeBytes, TakenAt: row.TakenAt,
			PreMigration: &preMigration,
		}
	}
	return views
}
