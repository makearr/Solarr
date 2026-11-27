package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// LibraryHealth is the library.db aggregate report; root capacity is computed in the handler from settings.Cache instead.
type LibraryHealth interface {
	Report(ctx context.Context) (LibraryHealthReport, error)
}

// LibraryHealthReport is what one GetLibraryHealth call answers.
type LibraryHealthReport struct {
	UnprobedCount   int
	UnprobedFiles   []UnprobedFile
	UnmatchedCount  int
	IncompleteCount int
	IncompleteWorks []IncompleteWork
	BlockedPresent  []BlockedPresent
}

// UnprobedFile is one file with no measured attributes yet (ADR-39).
type UnprobedFile struct {
	RootFolderID int64
	RelPath      string
	// FFprobeOutput is what ffprobe printed the last time it could not read the file; empty when it never failed.
	FFprobeOutput string
}

// IncompleteWork is one monitored work still short of its own required track count.
type IncompleteWork struct {
	WorkID                      int64
	Title                       string
	CategoryID                  int64
	State                       string
	RequiredCount, PresentCount int64
}

// BlockedPresent is one blocked hash (ADR-53 part 2) a scan found still
// sitting in the library -- Invariant 83: reported, never touched.
type BlockedPresent struct {
	BlockedContentID int64
	Reason           string
	Title            string
	MediaObjectID    int64
	RootFolderID     int64
	RelPath          string
}

// CategoryOfflineFiles is a category with files on an offline root, with its real total and the count currently unreachable.
type CategoryOfflineFiles struct {
	CategoryID   int64
	Name         string
	TotalFiles   int64
	OfflineFiles int64
}

// GetLibraryHealth answers todo 4.8's own aggregate report: probe coverage,
// unmatched files, incomplete works, roots near their reserve, and blocked
// hashes found in the library (ADR-53, Invariant 83).
func (s *Server) GetLibraryHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.libraryHealth == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "library health is not available")
		return
	}

	report, err := s.libraryHealth.Report(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	writeJSON(w, s.logger, http.StatusOK, gen.LibraryHealth{
		UnprobedCount:          report.UnprobedCount,
		UnprobedFiles:          unprobedFilesView(report.UnprobedFiles),
		UnmatchedCount:         report.UnmatchedCount,
		IncompleteCount:        report.IncompleteCount,
		IncompleteWorks:        incompleteWorksView(report.IncompleteWorks),
		RootsNearReserve:       s.rootsNearReserve(),
		BlockedPresent:         blockedPresentView(report.BlockedPresent),
		CategoriesOfflineFiles: categoriesOfflineFilesView(s.categoriesWithOfflineFiles(r.Context(), access)),
	})
}

// categoriesWithOfflineFiles joins visible categories (ADR-27) with the reconciled files counters in Go (ADR-25), returning only those with offline files.
func (s *Server) categoriesWithOfflineFiles(ctx context.Context, access authz.Access) []CategoryOfflineFiles {
	if s.librarySize == nil {
		return nil
	}
	var affected []CategoryOfflineFiles
	for _, category := range access.Categories(settings.PermissionRead) {
		values, err := s.librarySize.Counters(ctx, "category:"+strconv.FormatInt(category.ID, 10))
		if err != nil {
			s.logger.WarnContext(ctx, "library health: category counters unavailable",
				"category_id", category.ID, "error", err)
			continue
		}
		offline := values["files_offline"]
		if offline <= 0 {
			continue
		}
		affected = append(affected, CategoryOfflineFiles{
			CategoryID: category.ID, Name: category.Name,
			TotalFiles: values["files"], OfflineFiles: offline,
		})
	}
	return affected
}

func categoriesOfflineFilesView(rows []CategoryOfflineFiles) []gen.CategoryOfflineFiles {
	// Required (not omitempty): an empty array, not an absent key, matching
	// rootsNearReserve's own pattern -- a required array field that
	// serialises as JSON null instead of [] has broken a frontend before.
	views := make([]gen.CategoryOfflineFiles, 0, len(rows))
	for _, row := range rows {
		views = append(views, gen.CategoryOfflineFiles{
			CategoryId: row.CategoryID, Name: row.Name, TotalFiles: row.TotalFiles, OfflineFiles: row.OfflineFiles,
		})
	}
	return views
}

func unprobedFilesView(files []UnprobedFile) []gen.UnprobedFile {
	views := make([]gen.UnprobedFile, len(files))
	for i, f := range files {
		views[i] = gen.UnprobedFile{RootFolderId: f.RootFolderID, RelPath: f.RelPath, FfprobeOutput: f.FFprobeOutput}
	}
	return views
}

func incompleteWorksView(works []IncompleteWork) []gen.IncompleteWork {
	views := make([]gen.IncompleteWork, len(works))
	for i, w := range works {
		views[i] = gen.IncompleteWork{
			WorkId: w.WorkID, Title: w.Title, CategoryId: w.CategoryID, State: gen.IncompleteWorkState(w.State),
			RequiredCount: w.RequiredCount, PresentCount: w.PresentCount,
		}
	}
	return views
}

func blockedPresentView(present []BlockedPresent) []gen.BlockedPresent {
	views := make([]gen.BlockedPresent, len(present))
	for i, p := range present {
		views[i] = gen.BlockedPresent{
			BlockedContentId: p.BlockedContentID, Reason: p.Reason, Title: p.Title,
			MediaObjectId: p.MediaObjectID, RootFolderId: p.RootFolderID, RelPath: p.RelPath,
		}
	}
	return views
}

// rootsNearReserve names every root isNearReserve flags.
func (s *Server) rootsNearReserve() []gen.RootNearReserve {
	var near []gen.RootNearReserve
	for _, folder := range s.auth.Settings.Snapshot().RootFolders() {
		if !isNearReserve(folder.Online, folder.FreeBytes, folder.ReserveBytes()) {
			continue
		}
		near = append(near, gen.RootNearReserve{
			RootFolderId: folder.ID, Path: folder.Path,
			FreeBytes: *folder.FreeBytes, MinFreeBytes: folder.ReserveBytes(),
		})
	}
	if near == nil {
		near = []gen.RootNearReserve{}
	}
	return near
}

// isNearReserve: free space within 10% of the root's reserve; no reserve, an offline root or an unmeasured one never warns.
func isNearReserve(online bool, freeBytes *int64, minFreeBytes int64) bool {
	if !online || minFreeBytes <= 0 || freeBytes == nil {
		return false
	}
	return *freeBytes < (minFreeBytes*11)/10
}
