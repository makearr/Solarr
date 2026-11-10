package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/platform/config"
)

// DiskPath is where one path lives and which disk group it was placed in.
type DiskPath struct {
	Path, MountPoint, FSType, Source, Filesystem, Pool, Disk, Evidence string
	DetectedGroup, Group                                               string
	Overridden                                                         bool
	TotalBytes, FreeBytes                                              int64
}

// DiskLatency is the latest probe of one disk group; MeasuredAt is zero when never measured.
type DiskLatency struct {
	Group          string
	LastMS, MeanMS float64
	Samples        int
	MeasuredAtUnix int64
	Error          string
}

// DiskInotify is the host's watch limit and Solarr's share of it.
type DiskInotify struct {
	MaxUserWatches     int64
	BudgetWatches      int64
	UsedByUs           int64
	Directories        int64
	RecommendedWatches int64
}

// DiskScanRoot is what the scan schedule thinks of one root folder.
type DiskScanRoot struct {
	RootFolderID int64
	Path         string
	LastScanUnix int64
	Watching     bool
	CoveredDepth int
	Due          bool
	Why          string
}

// DiskScan is the scan schedule: the person's interval (0 is automatic) and each root's standing.
type DiskScan struct {
	IntervalHours int64
	Roots         []DiskScanRoot
}

// DiskReport is the Settings -> Disk page.
type DiskReport struct {
	Paths     []DiskPath
	Latencies []DiskLatency
	Inotify   DiskInotify
	Scan      DiskScan
}

// DiskManager serves the expert Disk page.
type DiskManager interface {
	Report(ctx context.Context) (DiskReport, error)
	// SetOverrides replaces the admin's corrections (path -> group label; blank drops one).
	SetOverrides(ctx context.Context, overrides map[string]string) error
	// Measure probes every disk group once, now, whatever the minimum interval.
	Measure(ctx context.Context) error
	// SetScanInterval stores the hours between scans; 0 is automatic.
	SetScanInterval(ctx context.Context, hours int64) error
}

func (s *Server) diskManagerOrProblem(w http.ResponseWriter) (DiskManager, bool) {
	if s.diskManager == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "disk settings are not available")
		return nil, false
	}
	return s.diskManager, true
}

// GetDiskSettings answers the detected layout, the latest latencies and the inotify budget.
func (s *Server) GetDiskSettings(w http.ResponseWriter, r *http.Request) {
	manager, ok := s.diskManagerOrProblem(w)
	if !ok {
		return
	}
	s.writeDiskReport(w, r, manager)
}

func (s *Server) writeDiskReport(w http.ResponseWriter, r *http.Request, manager DiskManager) {
	report, err := manager.Report(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, diskReportView(report))
}

// SetDiskOverrides replaces the corrections to the detected layout. Administrator only.
func (s *Server) SetDiskOverrides(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	manager, ok := s.diskManagerOrProblem(w)
	if !ok {
		return
	}
	var body gen.DiskOverrides
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected {\"groups\": {path: label}}")
		return
	}
	if err := manager.SetOverrides(r.Context(), body.Groups); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the disk groups", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	s.writeDiskReport(w, r, manager)
}

// MeasureDisks probes every disk group once. Administrator only.
func (s *Server) MeasureDisks(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	manager, ok := s.diskManagerOrProblem(w)
	if !ok {
		return
	}
	if err := manager.Measure(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.writeDiskReport(w, r, manager)
}

// SetDiskScan changes the hours between library scans. Administrator only.
func (s *Server) SetDiskScan(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	manager, ok := s.diskManagerOrProblem(w)
	if !ok {
		return
	}
	var body gen.DiskScanChange
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected {\"interval_hours\": number}")
		return
	}
	if err := manager.SetScanInterval(r.Context(), body.IntervalHours); err != nil {
		var problems config.Problems
		if errors.As(err, &problems) {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", problems.Report("the scan interval", "Nothing was changed."))
			return
		}
		writeError(w, s.logger, err)
		return
	}
	s.writeDiskReport(w, r, manager)
}

func diskReportView(report DiskReport) gen.DiskReport {
	paths := make([]gen.DiskPath, 0, len(report.Paths))
	for _, p := range report.Paths {
		paths = append(paths, gen.DiskPath{
			Path: p.Path, MountPoint: p.MountPoint, FsType: p.FSType, Source: p.Source, Filesystem: p.Filesystem,
			Pool: p.Pool, Disk: p.Disk, Evidence: p.Evidence, DetectedGroup: p.DetectedGroup, Group: p.Group,
			Overridden: p.Overridden, TotalBytes: p.TotalBytes, FreeBytes: p.FreeBytes,
		})
	}
	latencies := make([]gen.DiskLatency, 0, len(report.Latencies))
	for _, l := range report.Latencies {
		latency := gen.DiskLatency{Group: l.Group, LastMs: l.LastMS, MeanMs: l.MeanMS, Samples: l.Samples, Error: l.Error}
		if l.MeasuredAtUnix != 0 {
			at := l.MeasuredAtUnix
			latency.MeasuredAt = &at
		}
		latencies = append(latencies, latency)
	}
	roots := make([]gen.DiskScanRoot, 0, len(report.Scan.Roots))
	for _, r := range report.Scan.Roots {
		root := gen.DiskScanRoot{
			RootFolderId: r.RootFolderID, Path: r.Path, Watching: r.Watching, CoveredDepth: r.CoveredDepth, Due: r.Due, Why: r.Why,
		}
		if r.LastScanUnix != 0 {
			at := r.LastScanUnix
			root.LastScan = &at
		}
		roots = append(roots, root)
	}
	return gen.DiskReport{
		Paths: paths, Latencies: latencies,
		Inotify: gen.DiskInotify{
			MaxUserWatches: report.Inotify.MaxUserWatches, BudgetWatches: report.Inotify.BudgetWatches, UsedByUs: report.Inotify.UsedByUs,
			Directories: report.Inotify.Directories, RecommendedWatches: report.Inotify.RecommendedWatches,
		},
		Scan: gen.DiskScan{IntervalHours: report.Scan.IntervalHours, Roots: roots},
	}
}
