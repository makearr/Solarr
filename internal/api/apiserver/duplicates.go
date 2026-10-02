package apiserver

import (
	"context"
	"net/http"
	"path"

	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// DuplicateGroupRow is one track a library holds more than once and which copy to keep.
type DuplicateGroupRow struct {
	Artist, Title string
	Copies        []DuplicateCopyRow
	// Keep indexes Copies; KeepReason says why that one.
	Keep       int
	KeepReason string
}

// DuplicateCopyRow is one stored file of the track.
type DuplicateCopyRow struct {
	MediaObjectID, WorkID int64
	Album                 string
	SizeBytes, DurationMs int64
	BitrateKbps           int64
	AudioCodec            string
	Lossless              bool
	Paths                 []DuplicatePathRow
}

// DuplicatePathRow is one path of a copy.
type DuplicatePathRow struct {
	RootFolderID int64
	RelPath      string
}

// DuplicateReader finds the tracks a library holds more than once.
type DuplicateReader interface {
	Duplicates(ctx context.Context, categoryID int64, rootFolderIDs []int64, limit int) ([]DuplicateGroupRow, error)
}

// duplicateGroupLimit bounds one report.
const duplicateGroupLimit = 300

func (s *Server) routeGetCategoryDuplicates(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategoryDuplicates(w, r, id)
	}
}

// GetCategoryDuplicates answers one library's duplicate tracks; a library the caller cannot read is 404.
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
	groups, err := s.duplicates.Duplicates(r.Context(), id, roots, duplicateGroupLimit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type copyView struct {
		MediaObjectID int64    `json:"media_object_id"`
		WorkID        int64    `json:"work_id"`
		Album         string   `json:"album"`
		SizeBytes     int64    `json:"size_bytes"`
		DurationMs    int64    `json:"duration_ms"`
		BitrateKbps   int64    `json:"bitrate_kbps"`
		AudioCodec    string   `json:"audio_codec"`
		Lossless      bool     `json:"lossless"`
		Keep          bool     `json:"keep"`
		Paths         []string `json:"paths"`
	}
	type groupView struct {
		Artist           string     `json:"artist"`
		Title            string     `json:"title"`
		KeepReason       string     `json:"keep_reason"`
		ReclaimableBytes int64      `json:"reclaimable_bytes"`
		Copies           []copyView `json:"copies"`
	}
	report := struct {
		ReclaimableBytes int64       `json:"reclaimable_bytes"`
		Groups           []groupView `json:"groups"`
	}{Groups: make([]groupView, 0, len(groups))}
	for _, group := range groups {
		view := groupView{Artist: group.Artist, Title: group.Title, KeepReason: group.KeepReason}
		for index, copy := range group.Copies {
			paths := make([]string, len(copy.Paths))
			for i, one := range copy.Paths {
				paths[i] = path.Join(rootPath[one.RootFolderID], one.RelPath)
			}
			if index != group.Keep {
				view.ReclaimableBytes += copy.SizeBytes
			}
			view.Copies = append(view.Copies, copyView{
				MediaObjectID: copy.MediaObjectID, WorkID: copy.WorkID, Album: copy.Album, SizeBytes: copy.SizeBytes,
				DurationMs: copy.DurationMs, BitrateKbps: copy.BitrateKbps, AudioCodec: copy.AudioCodec, Lossless: copy.Lossless,
				Keep: index == group.Keep, Paths: paths,
			})
		}
		report.Groups = append(report.Groups, view)
		report.ReclaimableBytes += view.ReclaimableBytes
	}
	writeJSON(w, s.logger, http.StatusOK, report)
}
