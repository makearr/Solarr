package apiserver

import (
	"context"
	"net/http"
	"path"

	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// DuplicateGroupRow is files believed to be one file stored more than once; each copy lists the paths reaching it,
// as a root folder id and the path under it.
type DuplicateGroupRow struct {
	SizeBytes int64
	Copies    []DuplicateCopyRow
}

// DuplicateCopyRow is one stored file.
type DuplicateCopyRow struct {
	MediaObjectID int64
	Paths         []DuplicatePathRow
}

// DuplicatePathRow is one path of a copy.
type DuplicatePathRow struct {
	RootFolderID int64
	RelPath      string
}

// DuplicateReader finds likely copies under some root folders.
type DuplicateReader interface {
	Duplicates(ctx context.Context, rootFolderIDs []int64, limit int) ([]DuplicateGroupRow, error)
}

// duplicateGroupLimit bounds one report.
const duplicateGroupLimit = 300

func (s *Server) routeGetCategoryDuplicates(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategoryDuplicates(w, r, id)
	}
}

// GetCategoryDuplicates answers one library's likely duplicate files; a library the caller cannot read is 404.
func (s *Server) GetCategoryDuplicates(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.duplicates == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "duplicates are not available")
		return
	}
	snapshot := s.auth.Settings.Snapshot()
	folders := snapshot.RootFoldersForCategory(id)
	roots := make([]int64, len(folders))
	rootPath := make(map[int64]string, len(folders))
	for i, folder := range folders {
		roots[i], rootPath[folder.ID] = folder.ID, folder.Path
	}
	groups, err := s.duplicates.Duplicates(r.Context(), roots, duplicateGroupLimit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type copyView struct {
		MediaObjectID int64    `json:"media_object_id"`
		Paths         []string `json:"paths"`
	}
	type groupView struct {
		SizeBytes        int64      `json:"size_bytes"`
		ReclaimableBytes int64      `json:"reclaimable_bytes"`
		Copies           []copyView `json:"copies"`
	}
	report := struct {
		ReclaimableBytes int64       `json:"reclaimable_bytes"`
		Groups           []groupView `json:"groups"`
	}{Groups: make([]groupView, 0, len(groups))}
	for _, group := range groups {
		view := groupView{SizeBytes: group.SizeBytes, ReclaimableBytes: group.SizeBytes * int64(len(group.Copies)-1)}
		for _, copy := range group.Copies {
			paths := make([]string, len(copy.Paths))
			for i, one := range copy.Paths {
				paths[i] = path.Join(rootPath[one.RootFolderID], one.RelPath)
			}
			view.Copies = append(view.Copies, copyView{MediaObjectID: copy.MediaObjectID, Paths: paths})
		}
		report.Groups = append(report.Groups, view)
		report.ReclaimableBytes += view.ReclaimableBytes
	}
	writeJSON(w, s.logger, http.StatusOK, report)
}
