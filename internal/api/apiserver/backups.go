package apiserver

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
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
	// Latest opens the newest bundle; ErrNoBackup when there is none. The caller closes it.
	Latest(ctx context.Context) (BackupDownload, error)
}

// ErrNoBackup says no backup bundle has been taken yet.
var ErrNoBackup = errors.New("apiserver: there is no backup yet")

// BackupDownload is one backup bundle opened for reading.
type BackupDownload struct {
	Name      string
	SizeBytes int64
	Content   io.ReadCloser
}

// DownloadLatestBackup streams the newest backup bundle as a file.
func (s *Server) DownloadLatestBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.backups == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "backups are not available")
		return
	}
	download, err := s.backups.Latest(r.Context())
	if errors.Is(err, ErrNoBackup) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "there is no backup yet")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	defer func() { _ = download.Content.Close() }()
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.Name}))
	w.Header().Set("Content-Length", strconv.FormatInt(download.SizeBytes, 10))
	if _, err := io.Copy(w, download.Content); err != nil {
		s.logger.Warn("backup download interrupted", "error", err)
	}
}

// ListBackups answers the backup files on disk, newest first.
func (s *Server) ListBackups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
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
	if !s.decodeBody(w, r, maxUserBody, &request, "expected a JSON object with mode, keep_count and keep_days") {
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
