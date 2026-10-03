package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// StatisticsFigure is one rollup row of a library.
type StatisticsFigure struct {
	Key   string
	Value float64
	Count int64
}

// StatisticsView is what the last rollup computed for one library; ComputedAt is zero before the first run.
type StatisticsView struct {
	ComputedAt         time.Time
	WindowDays         int
	FilesByCodec       []StatisticsFigure
	FilesByVideoCodec  []StatisticsFigure
	FilesByResolution  []StatisticsFigure
	FilesByHDR         []StatisticsFigure
	FilesByAudio       []StatisticsFigure
	AlbumsByState      []StatisticsFigure
	DownloadsByState   []StatisticsFigure
	GrowthByMonth      []StatisticsFigure
	DecisionsByOutcome []StatisticsFigure
}

// StatisticsReader answers a library's statistics from stat_rollup (section 21.12).
type StatisticsReader interface {
	CategoryStatistics(ctx context.Context, categoryID int64) (StatisticsView, error)
}

func (s *Server) routeGetCategoryStatistics(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategoryStatistics(w, r, id)
	}
}

// GetCategoryStatistics answers one library's rolled-up figures; a library the caller cannot read is 404.
func (s *Server) GetCategoryStatistics(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.statistics == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "statistics are not available")
		return
	}
	view, err := s.statistics.CategoryStatistics(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	out := gen.CategoryStatistics{
		WindowDays:         view.WindowDays,
		FilesByCodec:       make([]gen.StatisticsFiles, len(view.FilesByCodec)),
		AlbumsByState:      counts(view.AlbumsByState),
		DownloadsByState:   counts(view.DownloadsByState),
		DecisionsByOutcome: counts(view.DecisionsByOutcome),
		GrowthByMonth:      make([]gen.StatisticsFiles, len(view.GrowthByMonth)),
	}
	for index, figure := range view.GrowthByMonth {
		out.GrowthByMonth[index] = gen.StatisticsFiles{Key: figure.Key, Files: figure.Count, Bytes: int64(figure.Value)}
	}
	for index, figure := range view.FilesByCodec {
		out.FilesByCodec[index] = gen.StatisticsFiles{Key: figure.Key, Files: figure.Count, Bytes: int64(figure.Value)}
	}
	files := func(figures []StatisticsFigure) *[]gen.StatisticsFiles {
		if len(figures) == 0 {
			return nil
		}
		out := make([]gen.StatisticsFiles, len(figures))
		for index, figure := range figures {
			out[index] = gen.StatisticsFiles{Key: figure.Key, Files: figure.Count, Bytes: int64(figure.Value)}
		}
		return &out
	}
	out.FilesByVideoCodec, out.FilesByResolution = files(view.FilesByVideoCodec), files(view.FilesByResolution)
	out.FilesByHdr, out.FilesByAudio = files(view.FilesByHDR), files(view.FilesByAudio)
	if !view.ComputedAt.IsZero() {
		out.ComputedAt = &view.ComputedAt
	}
	writeJSON(w, s.logger, http.StatusOK, out)
}

func counts(figures []StatisticsFigure) []gen.StatisticsCount {
	out := make([]gen.StatisticsCount, len(figures))
	for index, figure := range figures {
		out[index] = gen.StatisticsCount{Key: figure.Key, Count: figure.Count}
	}
	return out
}
