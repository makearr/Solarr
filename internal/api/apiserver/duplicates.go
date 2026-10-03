package apiserver

import (
	"context"
	"encoding/json"
	"net/http"
	"path"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
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

// DuplicateDrop is one copy to give up, and how: "link" turns each of its paths into a link to the kept copy, so the
// folders it was in stay complete; "recycle" moves it to the recycle bin.
type DuplicateDrop struct {
	MediaObjectID int64
	Action        string
}

// DuplicateOutcome is what a resolution did.
type DuplicateOutcome struct {
	Linked, Recycled int
	FreedBytes       int64
	// Problems say, one line each, what was left alone and why.
	Problems []string
}

// DuplicateResolver acts on a group of copies: keeps one and gives up the rest.
type DuplicateResolver interface {
	ResolveDuplicates(ctx context.Context, categoryID, keepMediaObjectID int64, drops []DuplicateDrop) (DuplicateOutcome, error)
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

func (s *Server) routeResolveCategoryDuplicates(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.ResolveCategoryDuplicates(w, r, id)
	}
}

// ResolveCategoryDuplicates keeps the named copy of a track and links or recycles the others; manage access to the library.
func (s *Server) ResolveCategoryDuplicates(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.duplicateResolver == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "duplicates cannot be resolved here")
		return
	}
	var body gen.ResolveCategoryDuplicatesJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil || len(body.Drop) == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "keep and at least one copy to drop are required")
		return
	}
	drops := make([]DuplicateDrop, len(body.Drop))
	for i, drop := range body.Drop {
		drops[i] = DuplicateDrop{MediaObjectID: drop.MediaObjectId, Action: string(drop.Action)}
	}
	outcome, err := s.duplicateResolver.ResolveDuplicates(r.Context(), id, body.KeepMediaObjectId, drops)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	problems := outcome.Problems
	if problems == nil {
		problems = []string{}
	}
	writeJSON(w, s.logger, http.StatusOK, gen.DuplicateResolution{
		Linked: outcome.Linked, Recycled: outcome.Recycled, FreedBytes: outcome.FreedBytes, Problems: problems,
	})
}
