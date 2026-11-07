package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
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
	// ImagesChanged applies new image figures to the pictures made from then on.
	ImagesChanged()
	// StartRecompress begins a recompress of the stored pictures; false when one is running.
	StartRecompress(ctx context.Context) bool
	// RecompressStatus is the running or last recompress.
	RecompressStatus() RecompressStatus
}

// RecompressStatus is how a recompress of stored pictures stands.
type RecompressStatus struct {
	Running                         bool
	Done, Total, SavedBytes, Failed int64
	StartedAt, FinishedAt           *time.Time
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

// GetImageSettings answers the picture figures and the state of a recompress.
func (s *Server) GetImageSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.databaseAvailable(w) {
		return
	}
	writeJSON(w, s.logger, http.StatusOK, s.imageSettingsView())
}

// SetImageSettings changes the picture figures.
func (s *Server) SetImageSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.databaseAvailable(w) {
		return
	}
	var body gen.ImageSettings
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected the four picture figures")
		return
	}
	err := s.auth.Registry.LookupAll(settings.KeyCoverMaxEdge, settings.KeyCoverQuality, settings.KeyPreviewMaxEdge, settings.KeyPreviewQuality)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	err = s.auth.Settings.SetImageSettings(r.Context(), s.auth.Clock, settings.ImageSettings{
		CoverMaxEdge: body.CoverMaxEdge, CoverQuality: body.CoverQuality, PreviewMaxEdge: body.PreviewMaxEdge, PreviewQuality: body.PreviewQuality,
	})
	var problems config.Problems
	if errors.As(err, &problems) {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The figures have problems", problems.Report("the picture figures", "Nothing was changed."))
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.databaseAdmin.ImagesChanged()
	writeJSON(w, s.logger, http.StatusOK, s.imageSettingsView())
}

// RecompressImages starts a recompress of the stored pictures.
func (s *Server) RecompressImages(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) || !s.databaseAvailable(w) {
		return
	}
	if !s.databaseAdmin.StartRecompress(r.Context()) {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Already running", "a recompress is already going")
		return
	}
	writeJSON(w, s.logger, http.StatusAccepted, s.imageSettingsView())
}

func (s *Server) databaseAvailable(w http.ResponseWriter) bool {
	if s.databaseAdmin == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the database page is not available")
		return false
	}
	return true
}

func (s *Server) imageSettingsView() gen.ImageSettings {
	images := s.auth.Settings.Snapshot().ImageSettings()
	view := gen.ImageSettings{
		CoverMaxEdge: images.CoverMaxEdge, CoverQuality: images.CoverQuality,
		PreviewMaxEdge: images.PreviewMaxEdge, PreviewQuality: images.PreviewQuality,
	}
	status := s.databaseAdmin.RecompressStatus()
	if status.Running || status.StartedAt != nil {
		view.Recompress = &struct {
			Done       int64      `json:"done"`
			Failed     int64      `json:"failed"`
			FinishedAt *time.Time `json:"finished_at,omitempty"`
			Running    bool       `json:"running"`
			SavedBytes int64      `json:"saved_bytes"`
			StartedAt  *time.Time `json:"started_at,omitempty"`
			Total      int64      `json:"total"`
		}{
			Done: status.Done, Failed: status.Failed, FinishedAt: status.FinishedAt, Running: status.Running,
			SavedBytes: status.SavedBytes, StartedAt: status.StartedAt, Total: status.Total,
		}
	}
	return view
}
