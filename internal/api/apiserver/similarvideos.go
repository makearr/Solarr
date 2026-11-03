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

// SimilarVideoRow is one video of a group that looks alike.
type SimilarVideoRow struct {
	MediaObjectID, SizeBytes int64
	DurationMs               int64
	Width, Height            int64
	RootFolderID             int64
	RelPath                  string
}

// SimilarVideoGroupRow is videos whose perceptual hashes are within MaxDistance bits of a neighbour.
type SimilarVideoGroupRow struct {
	MaxDistance int
	Videos      []SimilarVideoRow
}

// SimilarVideoReader finds the videos held in more than one encode; level is one of similarLevels.
type SimilarVideoReader interface {
	SimilarVideos(ctx context.Context, rootFolderIDs []int64, level string, limit int) ([]SimilarVideoGroupRow, error)
}

// similarVideoGroupLimit bounds one report.
const similarVideoGroupLimit = 300

// similarLevels are the accepted values of the level parameter, medium being the default.
var similarLevels = map[string]bool{"exact": true, "high": true, "medium": true, "low": true}

// similarLevelOf is the level a request asks for, and false when it names none that exists.
func similarLevelOf(r *http.Request) (string, bool) {
	level := r.URL.Query().Get("level")
	if level == "" {
		return "medium", true
	}
	return level, similarLevels[level]
}

func (s *Server) routeGetCategorySimilarVideos(w http.ResponseWriter, r *http.Request) {
	id, err := parsePathID(w, s, r, "id", "a category id must be a number")
	if err != nil {
		return
	}
	level, valid := similarLevelOf(r)
	if !valid {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "level must be exact, high, medium or low")
		return
	}
	l := gen.GetCategorySimilarVideosParamsLevel(level)
	s.GetCategorySimilarVideos(w, r, id, gen.GetCategorySimilarVideosParams{Level: &l})
}

func (s *Server) routeGetSimilarVideos(w http.ResponseWriter, r *http.Request) {
	level, valid := similarLevelOf(r)
	if !valid {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "level must be exact, high, medium or low")
		return
	}
	l := gen.GetSimilarVideosParamsLevel(level)
	s.GetSimilarVideos(w, r, gen.GetSimilarVideosParams{Level: &l})
}

// GetCategorySimilarVideos answers one library's groups of videos that look alike; a library the caller cannot read is 404.
func (s *Server) GetCategorySimilarVideos(w http.ResponseWriter, r *http.Request, id int64, params gen.GetCategorySimilarVideosParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	level := "medium"
	if params.Level != nil {
		level = string(*params.Level)
	}
	s.writeSimilarReport(w, r, s.auth.Settings.Snapshot().RootFoldersForCategory(id), level)
}

// GetSimilarVideos answers the groups of videos that look alike across every library; administrators only.
func (s *Server) GetSimilarVideos(w http.ResponseWriter, r *http.Request, params gen.GetSimilarVideosParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	level := "medium"
	if params.Level != nil {
		level = string(*params.Level)
	}
	s.writeSimilarReport(w, r, s.auth.Settings.Snapshot().RootFolders(), level)
}

func (s *Server) writeSimilarReport(w http.ResponseWriter, r *http.Request, folders []settings.RootFolder, level string) {
	if s.similarVideos == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "similar videos are not available")
		return
	}
	snapshot := s.auth.Settings.Snapshot()
	roots := make([]int64, len(folders))
	rootPath := make(map[int64]string, len(folders))
	rootCategory := make(map[int64]int64, len(folders))
	for i, folder := range folders {
		roots[i], rootPath[folder.ID] = folder.ID, folder.Path
		if categories := snapshot.CategoriesForRootFolder(folder.ID); len(categories) > 0 {
			rootCategory[folder.ID] = categories[0]
		}
	}
	groups, err := s.similarVideos.SimilarVideos(r.Context(), roots, level, similarVideoGroupLimit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type videoView struct {
		MediaObjectID int64  `json:"media_object_id"`
		SizeBytes     int64  `json:"size_bytes"`
		Path          string `json:"path"`
		DurationMs    int64  `json:"duration_ms"`
		Width         int64  `json:"width"`
		Height        int64  `json:"height"`
		CategoryID    int64  `json:"category_id"`
	}
	type groupView struct {
		MaxDistance int         `json:"max_distance"`
		Videos      []videoView `json:"videos"`
	}
	report := struct {
		HashingEnabled bool        `json:"hashing_enabled"`
		Limited        bool        `json:"limited"`
		Groups         []groupView `json:"groups"`
	}{HashingEnabled: snapshot.VideoPerceptualHash(), Groups: make([]groupView, 0, len(groups)), Limited: len(groups) >= similarVideoGroupLimit}
	for _, group := range groups {
		view := groupView{MaxDistance: group.MaxDistance, Videos: make([]videoView, 0, len(group.Videos))}
		for _, video := range group.Videos {
			view.Videos = append(view.Videos, videoView{
				MediaObjectID: video.MediaObjectID, SizeBytes: video.SizeBytes, Path: path.Join(rootPath[video.RootFolderID], video.RelPath),
				DurationMs: video.DurationMs, Width: video.Width, Height: video.Height, CategoryID: rootCategory[video.RootFolderID],
			})
		}
		report.Groups = append(report.Groups, view)
	}
	writeJSON(w, s.logger, http.StatusOK, report)
}

// ResolveSimilarVideos keeps one video of a group and merges or recycles the others, whichever library they are in;
// administrators only.
func (s *Server) ResolveSimilarVideos(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.duplicateResolver == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "duplicates cannot be resolved here")
		return
	}
	var body gen.ResolveSimilarVideosJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil || len(body.Drop) == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "keep and at least one copy to drop are required")
		return
	}
	drops := make([]DuplicateDrop, len(body.Drop))
	for i, drop := range body.Drop {
		drops[i] = DuplicateDrop{MediaObjectID: drop.MediaObjectId, Action: string(drop.Action)}
	}
	outcome, err := s.duplicateResolver.ResolveDuplicatesEverywhere(r.Context(), body.KeepMediaObjectId, drops)
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

// GetVideoHashing answers whether videos are hashed in the background.
func (s *Server) GetVideoHashing(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.logger, http.StatusOK, gen.VideoHashing{Enabled: s.auth.Settings.Snapshot().VideoPerceptualHash()})
}

// SetVideoHashing turns background video hashing on or off; the next probe sweep follows it.
func (s *Server) SetVideoHashing(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var request gen.VideoHashing
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUserBody)).Decode(&request); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "expected a JSON object with enabled")
		return
	}
	if err := s.auth.Settings.SetVideoPerceptualHash(r.Context(), s.auth.Clock, request.Enabled); err != nil {
		writeError(w, s.logger, err)
		return
	}
	s.GetVideoHashing(w, r)
}
