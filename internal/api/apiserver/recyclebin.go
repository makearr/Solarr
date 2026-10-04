package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// GetRecycleBinLimits answers the recycle bin's space bound.
func (s *Server) GetRecycleBinLimits(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, recycleBinLimitsView(s.auth.Settings.Snapshot().RecycleBinLimits()))
}

// UpdateRecycleBinLimits changes the recycle bin's space bound. Administrator only.
func (s *Server) UpdateRecycleBinLimits(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.RecycleBinLimits
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "expected max_bytes and max_percent_of_root")
		return
	}
	if err := s.auth.Registry.LookupAll(settings.KeyRecycleBinMaxBytes, settings.KeyRecycleBinMaxPercentOfRoot); err != nil {
		writeError(w, s.logger, err)
		return
	}
	limits := settings.RecycleBinLimits{MaxBytes: body.MaxBytes, MaxPercentOfRoot: body.MaxPercentOfRoot}
	if err := s.auth.Settings.SetRecycleBinLimits(r.Context(), s.auth.Clock, limits); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"The recycle bin limits have problems", problems.Report("the recycle bin limits", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, recycleBinLimitsView(limits))
}

func recycleBinLimitsView(limits settings.RecycleBinLimits) gen.RecycleBinLimits {
	return gen.RecycleBinLimits{MaxBytes: limits.MaxBytes, MaxPercentOfRoot: limits.MaxPercentOfRoot}
}

// RecycleBinEntryRow is one recycled file.
type RecycleBinEntryRow struct {
	ID                    int64
	OriginalPath, Reason  string
	SizeBytes             int64
	RecycledAt, ExpiresAt int64
}

// RecycleBinContents lists the recycle bins and empties them.
type RecycleBinContents interface {
	List(ctx context.Context) (entries []RecycleBinEntryRow, totalEntries, totalBytes int64, err error)
	Empty(ctx context.Context) (removed, freedBytes, failed int64, err error)
}

// ListRecycleBinEntries answers what the recycle bins hold, newest first.
func (s *Server) ListRecycleBinEntries(w http.ResponseWriter, r *http.Request) {
	if _, err := authz.FromContext(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.recycleContents == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the recycle bin is not available")
		return
	}
	entries, totalEntries, totalBytes, err := s.recycleContents.List(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := gen.RecycleBinContents{TotalEntries: totalEntries, TotalBytes: totalBytes, Entries: make([]gen.RecycleBinEntry, len(entries))}
	for i, entry := range entries {
		out.Entries[i] = gen.RecycleBinEntry{
			Id: entry.ID, OriginalPath: entry.OriginalPath, Reason: entry.Reason, SizeBytes: entry.SizeBytes,
			RecycledAt: entry.RecycledAt, ExpiresAt: entry.ExpiresAt,
		}
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

// EmptyRecycleBin removes everything in the recycle bins for good. Administrator only.
func (s *Server) EmptyRecycleBin(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.recycleContents == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the recycle bin is not available")
		return
	}
	removed, freed, failed, err := s.recycleContents.Empty(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.RecycleBinEmptied{Removed: removed, FreedBytes: freed, Failed: failed})
}
