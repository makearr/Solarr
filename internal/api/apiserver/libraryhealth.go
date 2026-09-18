package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// LibraryHealth answers todo 4.8's own aggregate report over library.db.
// Root folder capacity is not part of this interface: apiserver already
// holds settings.Cache directly (the same shape GetNetworkSettings uses),
// so "roots near reserve" is computed in the handler instead of routed
// through here.
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

// CategoryOfflineFiles is one category currently carrying files on a root
// declared offline (todo 4.18's own remaining item: "a category with an
// offline root reports its full count with an offline note"). TotalFiles is
// the category's real total regardless of reachability; OfflineFiles is how
// many of those sit on a root that is not reachable right now.
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

// categoriesWithOfflineFiles composes the caller's own visible categories
// (authz.Access.Categories, ADR-27 -- never the raw settings.Snapshot,
// Invariant 85/86: a category the caller cannot see must not appear here)
// with library.db's own per-category counter (internal/db/library.
// CategoryCounters, "files"/"files_offline") the same way rootsNearReserve
// already composes settings.db facts at the handler level rather than
// through the LibraryHealth interface (ADR-25: no cross-database join,
// composed in Go). Only categories actually carrying an offline file are
// returned -- the counter is reconciled periodically (ADR-28), never
// computed live here.
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
		views[i] = gen.UnprobedFile{RootFolderId: f.RootFolderID, RelPath: f.RelPath}
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
		if !isNearReserve(folder.Online, folder.FreeBytes, folder.MinFreeBytes) {
			continue
		}
		near = append(near, gen.RootNearReserve{
			RootFolderId: folder.ID, Path: folder.Path,
			FreeBytes: *folder.FreeBytes, MinFreeBytes: folder.MinFreeBytes,
		})
	}
	if near == nil {
		near = []gen.RootNearReserve{}
	}
	return near
}

// isNearReserve reports whether a root's own free space has fallen within
// 10% of its own configured reserve -- a root with no reserve configured
// (minFreeBytes <= 0) carries no such warning, an offline root is not a
// capacity concern, and one never yet measured (freeBytes nil) is skipped
// rather than guessed at.
func isNearReserve(online bool, freeBytes *int64, minFreeBytes int64) bool {
	if !online || minFreeBytes <= 0 || freeBytes == nil {
		return false
	}
	return *freeBytes < (minFreeBytes*11)/10
}
