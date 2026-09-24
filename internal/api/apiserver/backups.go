package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
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

// GetBackupRetention answers the Clean old backups task's rule.
func (s *Server) GetBackupRetention(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	retention := s.auth.Settings.Snapshot().BackupRetention()
	writeJSON(w, s.logger, http.StatusOK, gen.BackupRetention{
		Mode: gen.BackupRetentionMode(retention.Mode), KeepCount: retention.KeepCount, KeepDays: retention.KeepDays,
	})
}

// SetBackupRetention changes the rule; it takes effect on the task's next run.
func (s *Server) SetBackupRetention(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var request gen.BackupRetention
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected a JSON object with mode, keep_count and keep_days")
		return
	}
	err := s.auth.Registry.LookupAll(settings.KeyBackupRetention, settings.KeyBackupKeepCount, settings.KeyBackupKeepDays)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	retention := settings.BackupRetention{
		Mode: settings.BackupRetentionMode(request.Mode), KeepCount: request.KeepCount, KeepDays: request.KeepDays,
	}
	if err := s.auth.Settings.SetBackupRetention(r.Context(), s.auth.Clock, retention); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The retention has problems", problems.Report("the backup retention", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	s.GetBackupRetention(w, r)
}
