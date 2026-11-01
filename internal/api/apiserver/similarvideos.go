package apiserver

import (
	"context"
	"net/http"
	"path"

	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// SimilarVideoRow is one video of a group that looks alike.
type SimilarVideoRow struct {
	MediaObjectID, SizeBytes int64
	RootFolderID             int64
	RelPath                  string
}

// SimilarVideoGroupRow is videos whose perceptual hashes are within MaxDistance bits of a neighbour.
type SimilarVideoGroupRow struct {
	MaxDistance int
	Videos      []SimilarVideoRow
}

// SimilarVideoReader finds the videos a library holds in more than one encode.
type SimilarVideoReader interface {
	SimilarVideos(ctx context.Context, rootFolderIDs []int64, limit int) ([]SimilarVideoGroupRow, error)
}

// similarVideoGroupLimit bounds one report.
const similarVideoGroupLimit = 300

func (s *Server) routeGetCategorySimilarVideos(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategorySimilarVideos(w, r, id)
	}
}

// GetCategorySimilarVideos answers one library's groups of videos that look alike; a library the caller cannot read is 404.
func (s *Server) GetCategorySimilarVideos(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.similarVideos == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "similar videos are not available")
		return
	}
	folders := s.auth.Settings.Snapshot().RootFoldersForCategory(id)
	roots := make([]int64, len(folders))
	rootPath := make(map[int64]string, len(folders))
	for i, folder := range folders {
		roots[i], rootPath[folder.ID] = folder.ID, folder.Path
	}
	groups, err := s.similarVideos.SimilarVideos(r.Context(), roots, similarVideoGroupLimit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type videoView struct {
		MediaObjectID int64  `json:"media_object_id"`
		SizeBytes     int64  `json:"size_bytes"`
		Path          string `json:"path"`
	}
	type groupView struct {
		MaxDistance int         `json:"max_distance"`
		Videos      []videoView `json:"videos"`
	}
	report := struct {
		Limited bool        `json:"limited"`
		Groups  []groupView `json:"groups"`
	}{Groups: make([]groupView, 0, len(groups)), Limited: len(groups) >= similarVideoGroupLimit}
	for _, group := range groups {
		view := groupView{MaxDistance: group.MaxDistance, Videos: make([]videoView, 0, len(group.Videos))}
		for _, video := range group.Videos {
			view.Videos = append(view.Videos, videoView{
				MediaObjectID: video.MediaObjectID, SizeBytes: video.SizeBytes, Path: path.Join(rootPath[video.RootFolderID], video.RelPath),
			})
		}
		report.Groups = append(report.Groups, view)
	}
	writeJSON(w, s.logger, http.StatusOK, report)
}
