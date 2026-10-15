package apiserver

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
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
	SeriesSize         []StatisticsFigure
	SeriesGaps         []StatisticsFigure
	SizePerHour        []StatisticsFigure
	VideoBitrate       []StatisticsFigure
	AlbumsByState      []StatisticsFigure
	DownloadsByState   []StatisticsFigure
	GrowthByMonth      []StatisticsFigure
	DecisionsByOutcome []StatisticsFigure
	Searches           []StatisticsFigure
	MostSearched       []StatisticsFigure
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
	out.SeriesBySize, out.SeriesGaps = seriesFigures(view.SeriesSize, false), seriesFigures(view.SeriesGaps, true)
	out.SizePerHour = pictureFigures(view.SizePerHour, view.VideoBitrate)
	out.Searches, out.MostSearched = searchFigures(view.Searches), mostSearched(view.MostSearched)
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

// seriesFigures reads "<work id>|<title>" keys; for size lists value is bytes and count files, for gap lists value is
// the aired episodes missing and count the ones still to come.
func seriesFigures(figures []StatisticsFigure, gaps bool) *[]gen.StatisticsSeries {
	if len(figures) == 0 {
		return nil
	}
	out := make([]gen.StatisticsSeries, 0, len(figures))
	for _, figure := range figures {
		id, title, found := strings.Cut(figure.Key, "|")
		workID, err := strconv.ParseInt(id, 10, 64)
		if !found || err != nil {
			continue
		}
		row := gen.StatisticsSeries{SeriesId: workID, Title: title}
		if gaps {
			row.Missing, row.Unaired = int64Ptr(int64(figure.Value)), int64Ptr(figure.Count)
		} else {
			row.Bytes, row.Files = int64Ptr(int64(figure.Value)), int64Ptr(figure.Count)
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if gaps {
			return derefOr(out[i].Missing, 0) > derefOr(out[j].Missing, 0)
		}
		return derefOr(out[i].Bytes, 0) > derefOr(out[j].Bytes, 0)
	})
	return &out
}

func int64Ptr(value int64) *int64 { return &value }

// pictureFigures joins bytes per hour with the average video bitrate of the same resolution and codec.
func pictureFigures(perHour, bitrate []StatisticsFigure) *[]gen.StatisticsPicture {
	if len(perHour) == 0 {
		return nil
	}
	kbps := make(map[string]int64, len(bitrate))
	for _, figure := range bitrate {
		kbps[figure.Key] = int64(figure.Value)
	}
	out := make([]gen.StatisticsPicture, len(perHour))
	for i, figure := range perHour {
		out[i] = gen.StatisticsPicture{Key: figure.Key, Files: figure.Count, BytesPerHour: int64(figure.Value), VideoKbps: kbps[figure.Key]}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Files > out[j].Files })
	return &out
}

// searchFigures reads the "searched" and "found" counts; nil before the first search was logged.
func searchFigures(figures []StatisticsFigure) *gen.StatisticsSearches {
	if len(figures) == 0 {
		return nil
	}
	var out gen.StatisticsSearches
	for _, figure := range figures {
		switch figure.Key {
		case "searched":
			out.Searched = figure.Count
		case "found":
			out.Found = figure.Count
		}
	}
	return &out
}

// mostSearched reads "<work id>|<media ref>|<lead>|<title>" keys, value the searches and count the last one's time.
func mostSearched(figures []StatisticsFigure) *[]gen.StatisticsSearchedItem {
	if len(figures) == 0 {
		return nil
	}
	out := make([]gen.StatisticsSearchedItem, 0, len(figures))
	for _, figure := range figures {
		parts := strings.SplitN(figure.Key, "|", 4)
		if len(parts) != 4 {
			continue
		}
		workID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}
		item := gen.StatisticsSearchedItem{
			WorkId: workID, Title: parts[3], Searches: int64(figure.Value), LastSearchedAt: time.Unix(figure.Count, 0).UTC(),
		}
		if parts[1] != "" {
			item.MediaRef = &parts[1]
		}
		if parts[2] != "" {
			item.Lead = &parts[2]
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Searches > out[j].Searches })
	return &out
}
