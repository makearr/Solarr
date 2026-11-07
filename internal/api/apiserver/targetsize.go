package apiserver

import (
	"context"
	"math"
	"net/http"
	"sort"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
)

// furthestFileCount is how many of the files furthest from the target the Insights list shows.
const furthestFileCount = 15

// FileRateView is one video file of a library with what its bitrate is judged from.
type FileRateView struct {
	MediaObjectID, WorkID int64
	Title, MediaRef       string
	SizeBytes, DurationMs int64
	OverallBitrate        int64
}

// FileRates answers a library's primary video files. Defined here, implemented by the caller.
type FileRates func(ctx context.Context, categoryID int64) ([]FileRateView, error)

func (s *Server) routeGetCategoryTargetSize(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategoryTargetSize(w, r, id)
	}
}

// GetCategoryTargetSize judges every video file of a library against the bitrate its target expects.
func (s *Server) GetCategoryTargetSize(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(id, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.fileRates == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "file sizes are not available")
		return
	}
	out := gen.CategoryTargetSize{Furthest: []gen.TargetSizeFile{}}
	expected := 0.0
	if category.QualityTarget != nil {
		expected, _ = quality.ExpectedKbps(*category.QualityTarget)
	}
	if expected <= 0 {
		writeJSON(w, s.logger, http.StatusOK, out)
		return
	}
	out.ExpectedKbps = int64(math.Round(expected))
	rows, err := s.fileRates(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type judged struct {
		row   FileRateView
		ratio float64
	}
	var files []judged
	for _, row := range rows {
		actual := float64(row.OverallBitrate) / 1000
		if row.SizeBytes > 0 && row.DurationMs > 0 {
			actual = float64(row.SizeBytes) * 8 / float64(row.DurationMs)
		}
		ratio, _, ok := quality.SizeRatio(*category.QualityTarget, actual)
		if !ok {
			continue
		}
		files = append(files, judged{row, ratio})
		switch off := quality.Off(ratio); {
		case off < 0.10:
			out.OnTarget++
		case off < 0.25:
			out.ABitOff++
		case off < 0.50:
			out.FurtherOff++
		default:
			out.FarOff++
		}
	}
	out.Judged = int64(len(files))
	sort.SliceStable(files, func(a, b int) bool { return quality.Off(files[a].ratio) > quality.Off(files[b].ratio) })
	for _, file := range files[:min(furthestFileCount, len(files))] {
		entry := gen.TargetSizeFile{
			MediaObjectId: file.row.MediaObjectID, WorkId: file.row.WorkID, Title: file.row.Title, SizePct: int64(math.Round(file.ratio * 100)),
		}
		if file.row.MediaRef != "" {
			entry.MediaRef = &file.row.MediaRef
		}
		if file.row.SizeBytes > 0 {
			entry.SizeBytes = &file.row.SizeBytes
		}
		out.Furthest = append(out.Furthest, entry)
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}
